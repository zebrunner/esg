package handlers

import (
	"github.com/gin-gonic/gin"
	"github.com/zebrunner/esg/service"
	"github.com/zebrunner/esg/utils"
	"net/http"
)

type CreateUserModel struct {
	Username string `json:"username" binding:"required"`
}

type UserActivationModel struct {
	IsActive bool `json:"is_active" binding:"required" type:"bool"`
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
		"access-token": password,
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
		c.Error(err).SetType(gin.ErrorTypePublic)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"access-token": password,
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
			Status: http.StatusBadRequest,
			Message: "request body is invalid",
		}).SetType(gin.ErrorTypePublic).SetMeta(err.Error())
		return
	}

	err = service.ActivationUser(user, true)
	if err != nil {
		c.Error(err).SetType(gin.ErrorTypePublic)
		return
	}
	c.Status(http.StatusOK)
}
