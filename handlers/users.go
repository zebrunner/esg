package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/zebrunner/esg/service"
	"github.com/zebrunner/esg/utils"
)

type CreateUserModel struct {
	Username string  `json:"username" binding:"required"`
	Password *string `json:"password"`
}

type RefreshTokenModel struct {
	Password *string `json:"password"`
}

type UserActivationModel struct {
	IsActive bool `json:"isActive" type:"bool"`
}

func CreateUser(c *gin.Context) {
	body := CreateUserModel{}
	err := c.ShouldBindJSON(&body)
	if err != nil {
		c.Error(utils.InvalidApiRequestErr(err.Error())).SetType(gin.ErrorTypePublic)
		return
	}

	password, apiErr := service.CreateUser(body.Username, body.Password)
	if apiErr != nil {
		_ = c.Error(apiErr).SetType(gin.ErrorTypePublic)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"accessToken": password,
	})
}

func DeleteUser(c *gin.Context) {
	username := c.Param("username")
	apiErr := service.DeleteUser(username)
	if apiErr != nil {
		c.Error(apiErr).SetType(gin.ErrorTypePublic)
		return
	}

	c.Status(http.StatusOK)
}

func RefreshToken(c *gin.Context) {
	user := c.Param("username")
	body := RefreshTokenModel{}
	err := c.ShouldBindJSON(&body)
	if err != nil {
		_ = c.Error(utils.InvalidApiRequestErr(err.Error())).SetType(gin.ErrorTypePublic)
		return
	}

	password, apiErr := service.RefreshToken(user, body.Password)
	if apiErr != nil {
		c.Error(apiErr).SetType(gin.ErrorTypePublic)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"accessToken": password,
	})
}

func UserActivation(c *gin.Context) {
	user := c.Param("username")

	body := UserActivationModel{}
	err := c.ShouldBindJSON(&body)
	if err != nil {
		c.Error(utils.InvalidApiRequestErr(err.Error())).SetType(gin.ErrorTypePublic)
		return
	}

	err = service.ActivationUser(user, body.IsActive)
	if err != nil {
		c.Error(err).SetType(gin.ErrorTypePublic)
		return
	}

	c.Status(http.StatusOK)
}
