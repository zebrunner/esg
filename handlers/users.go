package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/db"
	"github.com/zebrunner/esg/utils"
)

type CreateUserModel struct {
	Username string `json:"username" binding:"required"`
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

	password, apiErr := db.CreateUser(body.Username)
	if apiErr != nil {
		_ = c.Error(apiErr).SetType(gin.ErrorTypePublic)
		return
	}

	log.WithField("username", body.Username).Info("successfully created new user")
	c.JSON(http.StatusOK, gin.H{
		"accessToken": password,
	})
}

func DeleteUser(c *gin.Context) {
	username := c.Param("username")
	apiErr := db.DeleteUser(username)
	if apiErr != nil {
		c.Error(apiErr).SetType(gin.ErrorTypePublic)
		return
	}

	log.WithField("username", username).Info("successfully deleted user")
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

	password, apiErr := db.RefreshToken(user, body.Password)
	if apiErr != nil {
		c.Error(apiErr).SetType(gin.ErrorTypePublic)
		return
	}

	log.WithField("username", user).Info("successfully refreshed token")
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

	apiErr := db.ActivationUser(user, body.IsActive)
	if apiErr != nil {
		c.Error(apiErr).SetType(gin.ErrorTypePublic)
		return
	}

	log.WithField("username", user).WithField("active", body.IsActive).Info("successfully changed user's activation")
	c.Status(http.StatusOK)
}
