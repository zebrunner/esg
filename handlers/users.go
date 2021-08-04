package handlers

import (
	"database/sql"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/zebrunner/esg/service"
	"github.com/zebrunner/esg/utils"
)

type CreateUserModel struct {
	Username string `json:"username" binding:"required"`
}

type UserActivationModel struct {
	IsActive bool `json:"isActive" type:"bool"`
}

func CreateUser(c *gin.Context) {
	body := CreateUserModel{}
	err := c.ShouldBindJSON(&body)
	if err != nil {
		c.Error(&utils.HTTPError{
			Status:  http.StatusBadRequest,
			Message: "request body is invalid",
		}).SetType(gin.ErrorTypePublic).SetMeta(err.Error())
		return
	}

	password, err := service.CreateUser(body.Username)
	if err != nil {
		c.Error(err).SetType(gin.ErrorTypePublic)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"accessToken": password,
	})
}

func DeleteUser(c *gin.Context) {
	username := c.Param("username")
	if username == "" {
		c.Error(&utils.HTTPError{
			Status:  http.StatusBadRequest,
			Message: "username parameter not found",
		})
		return
	}
	err := service.DeleteUser(username)
	if err != nil {
		if err == sql.ErrNoRows {
			c.Error(&utils.HTTPError{
				Status:  http.StatusNotFound,
				Message: "User not found",
			}).SetType(gin.ErrorTypePublic)
			return
		}
		c.Error(err).SetType(gin.ErrorTypePublic)
		return
	}
	c.Status(http.StatusOK)
}

func RefreshToken(c *gin.Context) {
	user := c.Param("username")
	if user == "" {
		c.Error(&utils.HTTPError{
			Status:  http.StatusBadRequest,
			Message: "username parameter not found",
		}).SetType(gin.ErrorTypePublic)
		return
	}
	password, err := service.RefreshToken(user)
	if err != nil {
		if err == sql.ErrNoRows {
			c.Error(&utils.HTTPError{
				Status:  http.StatusNotFound,
				Message: "User not found",
			}).SetType(gin.ErrorTypePublic)
			return
		}
		c.Error(err).SetType(gin.ErrorTypePublic)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"accessToken": password,
	})
}

func UserActivation(c *gin.Context) {
	user := c.Param("username")
	if user == "" {
		c.Error(&utils.HTTPError{
			Status:  http.StatusBadRequest,
			Message: "username parameter not found",
		}).SetType(gin.ErrorTypePublic)
		return
	}

	body := UserActivationModel{}
	err := c.ShouldBindJSON(&body)
	if err != nil {
		c.Error(&utils.HTTPError{
			Status:  http.StatusBadRequest,
			Message: "request body is invalid",
		}).SetType(gin.ErrorTypePublic).SetMeta(err.Error())
		return
	}

	err = service.ActivationUser(user, body.IsActive)
	if err != nil {
		if err == sql.ErrNoRows {
			c.Error(&utils.HTTPError{
				Status:  http.StatusNotFound,
				Message: "User not found",
			}).SetType(gin.ErrorTypePublic)
			return
		}
		c.Error(err).SetType(gin.ErrorTypePublic)
		return
	}
	c.Status(http.StatusOK)
}
