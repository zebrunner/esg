package service

import (
	"errors"
	"sync"

	"github.com/zebrunner/esg/config"
	"github.com/zebrunner/esg/utils"

	"github.com/jackc/pgtype"
	pgx "github.com/jackc/pgx/v4"
	_ "github.com/jackc/pgx/v4/stdlib"
	"github.com/sethvargo/go-password/password"
	log "github.com/sirupsen/logrus"
	"golang.org/x/crypto/bcrypt"
)

var mutexDB = &sync.RWMutex{}

type User struct {
	ID        int `db:"id"`
	Name      string
	Password  string
	IsActive  bool `db:"is_active"`
	CreatedAt pgtype.Timestamp
	UpdatedAt pgtype.Timestamp
	DeletedAt pgtype.Timestamp
}

func generatePassword() (string, error) {
	passwordLength := 16
	digitCount := 5
	symbolCount := 0
	noUpper := false
	allowRepeat := true
	return password.Generate(passwordLength, digitCount, symbolCount, noUpper, allowRepeat)
}

func CreateUser(name string, password *string) (string, error) {
	dbUser, _ := GetUser(name)
	if dbUser != nil {
		return "", utils.UserAlrExistsErr()
	}
	pwd, err := generatePassword()
	if err != nil {
		log.WithError(err).Error("Failed to generate password")
		pwd = *password
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(pwd), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}

	createQuery := `INSERT INTO users (name, password) VALUES ($1, $2)`
	_, err = config.DbConnection.Exec(createQuery, name, string(passwordHash))
	if err != nil {
		return "", err
	}

	log.WithField("user", name).Debug("User created successfully")
	return pwd, nil
}

func GetUser(name string) (*User, error) {
	getQuery := `SELECT id, name, password, is_active FROM users WHERE is_deleted = false AND name = $1`
	user := User{}
	mutexDB.Lock()
	err := config.DbConnection.Get(&user, getQuery, name)
	mutexDB.Unlock()
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, utils.UserNotFoundErr()
		} else {
			return nil, err
		}
	}

	return &user, nil
}

func ActivationUser(name string, isActive bool) error {
	user, err := GetUser(name)
	if err != nil {
		return err
	}
	invalidateQuery := `UPDATE users SET is_active = $1, updated_at = now() WHERE users.id = $2`
	_, err = config.DbConnection.Exec(invalidateQuery, isActive, user.ID)
	if err != nil {
		return err
	}
	return nil
}

func RefreshToken(name string, pwd *string) (string, error) {
	user, err := GetUser(name)
	if err != nil {
		return "", err
	}
	password := ""
	if pwd == nil {
		password, err = generatePassword()
		if err != nil {
			return "", err
		}
	} else {
		password = *pwd
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	refreshQuery := `UPDATE users SET password = $1, updated_at = now() WHERE id = $2`
	_, err = config.DbConnection.Exec(refreshQuery, passwordHash, user.ID)
	if err != nil {
		return "", err
	}
	return password, nil
}

func DeleteUser(name string) error {
	user, err := GetUser(name)
	if err != nil {
		return err
	}
	deleteQuery := `UPDATE users SET is_deleted=true WHERE id = $1`
	_, err = config.DbConnection.Exec(deleteQuery, user.ID)
	if err != nil {
		return err
	}
	return nil
}

func GetWorkspace(name string) (string, error) {
	if name == "" {
		return "", errors.New("failed to get auth credentials")
	}
	return name, nil
}

func CheckAuth(name, password string) utils.EsgError {
	if name == "" || password == "" {
		return utils.AuthNotFoundErr()
	}

	user, err := GetUser(name)
	if err != nil {
		if esgErr, ok := err.(utils.EsgError); ok {
			return esgErr
		} else {
			return utils.AuthErr()
		}
	}
	err = bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(password))
	if err != nil {
		return utils.AuthErr()
	}
	if !user.IsActive {
		return utils.DeactUserAccessErr()
	}
	return nil
}
