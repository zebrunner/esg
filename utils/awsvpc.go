package utils

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/aws/aws-sdk-go/service/ecs"
	log "github.com/sirupsen/logrus"
)

// WaitForPrivateIPWithRetry implements robust IP waiting logic for AWS VPC mode
// It handles the race condition where ENI attachment might be delayed
func WaitForPrivateIPWithRetry(ctx context.Context, task *ecs.Task, serviceStart time.Time, logEntry *log.Entry) (string, error) {
	const maxRetries = 10
	const retryInterval = 2 * time.Second

	logEntry.Debug("Starting robust private IP acquisition for AWS VPC")

	for attempt := 0; attempt < maxRetries; attempt++ {
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("context cancelled while waiting for private IP")
		default:
		}

		// Try to get the private IP from task attachments
		ip := GetAwsVpcTaskPrivateIPv4(task.Attachments)

		if ip != "" {
			logEntry.WithFields(log.Fields{
				"attempt":   attempt + 1,
				"privateIP": ip,
				"latency":   time.Since(serviceStart),
			}).Debug("Private IP acquired successfully")
			return ip, nil
		}

		// Log the attempt with detailed information
		logEntry.WithFields(log.Fields{
			"attempt":     attempt + 1,
			"maxRetries":  maxRetries,
			"retryIn":     retryInterval,
			"attachments": len(task.Attachments),
		}).Warn("Private IP not found in task attachments, retrying...")

		// Log attachment details for debugging
		for i, attachment := range task.Attachments {
			if attachment != nil {
				logEntry.WithFields(log.Fields{
					"attachmentIndex":  i,
					"attachmentId":     attachment.Id,
					"attachmentType":   attachment.Type,
					"attachmentStatus": attachment.Status,
					"detailsCount":     len(attachment.Details),
				}).Debug("Attachment details")

				// Log each detail for debugging
				for j, detail := range attachment.Details {
					if detail != nil && detail.Name != nil && detail.Value != nil {
						logEntry.WithFields(log.Fields{
							"attachmentIndex": i,
							"detailIndex":     j,
							"detailName":      *detail.Name,
							"detailValue":     *detail.Value,
						}).Debug("Attachment detail")
					}
				}
			}
		}

		// Wait before retrying, but respect context cancellation
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("context cancelled while waiting for private IP")
		case <-time.After(retryInterval):
			// Continue to next attempt
		}
	}

	return "", fmt.Errorf("failed to acquire private IP after %d attempts", maxRetries)
}

// ValidateNetworkReadiness performs comprehensive network connectivity validation
func ValidateNetworkReadiness(ctx context.Context, driverURL string, serviceStart time.Time, logEntry *log.Entry) error {
	logEntry.Debug("Starting network readiness validation")

	if driverURL == "" {
		return fmt.Errorf("driver URL is empty")
	}

	logEntry.WithField("driverURL", driverURL).Debug("Driver URL provided for validation")

	// Parse URL to get host
	parsedURL, err := url.Parse(driverURL)
	if err != nil {
		return fmt.Errorf("failed to parse driver URL: %v", err)
	}

	// Test 1: Validate TCP connectivity to driver port
	if err := validateTCPConnectivity(ctx, parsedURL.Host, logEntry); err != nil {
		return fmt.Errorf("TCP connectivity validation failed: %v", err)
	}

	// Test 2: Validate HTTP connectivity to driver endpoint
	if err := validateHTTPConnectivity(ctx, driverURL, logEntry); err != nil {
		return fmt.Errorf("HTTP connectivity validation failed: %v", err)
	}

	logEntry.WithFields(log.Fields{
		"driverURL": driverURL,
		"latency":   time.Since(serviceStart),
	}).Info("Network readiness validation completed successfully")

	return nil
}

// validateTCPConnectivity tests basic TCP connectivity to the driver port
func validateTCPConnectivity(ctx context.Context, host string, logEntry *log.Entry) error {
	const maxRetries = 5
	const retryInterval = 1 * time.Second

	logEntry.WithField("host", host).Debug("Starting TCP connectivity validation")

	for attempt := 0; attempt < maxRetries; attempt++ {
		select {
		case <-ctx.Done():
			return fmt.Errorf("context cancelled during TCP connectivity test")
		default:
		}

		// Create a timeout context for the connection attempt
		connCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()

		// Attempt to establish TCP connection
		conn, err := dialWithContext(connCtx, "tcp", host)
		if err == nil {
			conn.Close()
			logEntry.WithFields(log.Fields{
				"host":    host,
				"attempt": attempt + 1,
			}).Debug("TCP connectivity validation successful")
			return nil
		}

		logEntry.WithFields(log.Fields{
			"host":    host,
			"attempt": attempt + 1,
			"error":   err.Error(),
		}).Warn("TCP connectivity test failed, retrying...")

		// Wait before retrying
		select {
		case <-ctx.Done():
			return fmt.Errorf("context cancelled during TCP connectivity test")
		case <-time.After(retryInterval):
			// Continue to next attempt
		}
	}

	return fmt.Errorf("TCP connectivity validation failed after %d attempts", maxRetries)
}

// validateHTTPConnectivity tests HTTP connectivity to the driver endpoint
func validateHTTPConnectivity(ctx context.Context, url string, logEntry *log.Entry) error {
	const maxRetries = 3
	const retryInterval = 2 * time.Second

	logEntry.WithField("url", url).Debug("Starting HTTP connectivity validation")

	for attempt := 0; attempt < maxRetries; attempt++ {
		select {
		case <-ctx.Done():
			return fmt.Errorf("context cancelled during HTTP connectivity test")
		default:
		}

		reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()

		req, err := http.NewRequestWithContext(reqCtx, "GET", url, nil)
		if err != nil {
			logEntry.WithError(err).Warn("Failed to create HTTP request for connectivity test")
			continue
		}

		req.Header.Set("User-Agent", "ESG-NetworkValidator/1.0")
		req.Header.Set("Accept", "application/json")

		// Make HTTP request
		client := &http.Client{
			Timeout: 10 * time.Second,
		}

		resp, err := client.Do(req)
		if err != nil {
			logEntry.WithFields(log.Fields{
				"url":     url,
				"attempt": attempt + 1,
				"error":   err.Error(),
			}).Warn("HTTP connectivity test failed, retrying...")

			select {
			case <-ctx.Done():
				return fmt.Errorf("context cancelled during HTTP connectivity test")
			case <-time.After(retryInterval):
				// Continue to next attempt
			}
			continue
		}

		resp.Body.Close()

		// Accept any HTTP response (even 404) as it means the service is reachable
		logEntry.WithFields(log.Fields{
			"url":        url,
			"statusCode": resp.StatusCode,
			"attempt":    attempt + 1,
		}).Debug("HTTP connectivity validation successful")
		return nil
	}

	return fmt.Errorf("HTTP connectivity validation failed after %d attempts", maxRetries)
}

func dialWithContext(ctx context.Context, network, address string) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, network, address)
}
