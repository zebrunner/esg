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
		_ = c.Error(utils.InvalidReqBodyErr()).SetType(gin.ErrorTypePublic).SetMeta(err.Error())
		return
	}

	password, err := service.CreateUser(body.Username, body.Password)
	if err != nil {
		_ = c.Error(err).SetType(gin.ErrorTypePublic)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"accessToken": password,
	})
}

func DeleteUser(c *gin.Context) {
	username := c.Param("username")
	if username == "" {
		_ = c.Error(utils.ParamNotFoundErr("username")).SetType(gin.ErrorTypePublic)
		return
	}
	err := service.DeleteUser(username)
	if err != nil {
		_ = c.Error(err).SetType(gin.ErrorTypePublic)
		return
	}
	c.Status(http.StatusOK)
}

func RefreshToken(c *gin.Context) {
	user := c.Param("username")
	if user == "" {
		_ = c.Error(utils.ParamNotFoundErr("username")).SetType(gin.ErrorTypePublic)
		return
	}
	body := RefreshTokenModel{}
	err := c.ShouldBindJSON(&body)
	if err != nil {
		_ = c.Error(utils.InvalidReqBodyErr()).SetType(gin.ErrorTypePublic).SetMeta(err.Error())
		return
	}

	password, err := service.RefreshToken(user, body.Password)
	if err != nil {
		_ = c.Error(err).SetType(gin.ErrorTypePublic)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"accessToken": password,
	})
}

func UserActivation(c *gin.Context) {
	user := c.Param("username")
	if user == "" {
		_ = c.Error(utils.ParamNotFoundErr("username")).SetType(gin.ErrorTypePublic)
		return
	}

	body := UserActivationModel{}
	err := c.ShouldBindJSON(&body)
	if err != nil {
		_ = c.Error(utils.InvalidReqBodyErr()).SetType(gin.ErrorTypePublic).SetMeta(err.Error())
		return
	}

	err = service.ActivationUser(user, body.IsActive)
	if err != nil {
		_ = c.Error(err).SetType(gin.ErrorTypePublic)
		return
	}
	c.Status(http.StatusOK)
}
