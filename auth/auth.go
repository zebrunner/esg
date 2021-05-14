package auth

import (
	"net/http"

	"github.com/jackc/pgtype"
	_ "github.com/jackc/pgx/v4/stdlib"
	"github.com/jmoiron/sqlx"
	"github.com/sethvargo/go-password/password"
	"github.com/zebrunner/esg/webserver"
	"golang.org/x/crypto/bcrypt"
)

var (
	db *sqlx.DB
)

type Tenant struct {
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

func InitConnection(connectionString string) (*sqlx.DB, error) {
	client, err := sqlx.Open("pgx", connectionString)
	if err != nil {
		return nil, err
	}
	db = client
	return db, nil
}

func CreateTenant(name string) (string, error) {
	pwd, err := generatePassword()
	if err != nil {
		return "", err
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(pwd), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}

	createQuery := `INSERT INTO tenants (name, password) VALUES ($1, $2)`
	_, err = db.Exec(createQuery, name, string(passwordHash))
	if err != nil {
		return "", err
	}

	return pwd, nil
}

func GetTenant(name string) (*Tenant, error) {
	getQuery := `SELECT id, name, password, is_active FROM tenants WHERE deleted_at IS NULL AND name = $1`
	tenant := Tenant{}
	err := db.Get(&tenant, getQuery, name)
	if err != nil {
		if err.Error() == "sql: no rows in result set" {
			return nil, &webserver.HTTPError{
				Status:  404,
				Message: err.Error(),
			}
		} else {
			return nil, err
		}
	}
	return &tenant, nil
}

func ActivationTenant(name string, isActive bool) error {
	tenant, err := GetTenant(name)
	if err != nil {
		return err
	}
	invalidateQuery := `UPDATE tenants SET is_active = $1, updated_at = now() WHERE tenants.id = $2`
	_, err = db.Exec(invalidateQuery, isActive, tenant.ID)
	if err != nil {
		return err
	}
	return nil
}

func RefreshToken(name string) (string, error) {
	tenant, err := GetTenant(name)
	if err != nil {
		return "", err
	}
	password, err := generatePassword()
	if err != nil {
		return "", err
	}
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	refreshQuery := `UPDATE tenants SET password = $1, updated_at = now() WHERE id = $2`
	_, err = db.Exec(refreshQuery, passwordHash, tenant.ID)
	if err != nil {
		return "", err
	}
	return password, nil
}

func DeleteTenant(name string) error {
	tenant, err := GetTenant(name)
	if err != nil {
		return err
	}
	deleteQuery := `UPDATE tenants SET deleted_at = now() WHERE id = $1`
	db.Exec(deleteQuery, tenant.ID)
	return nil
}

func CheckAuth(name, password string) error {
	authenticationError := &webserver.HTTPError{
		Status:  http.StatusUnauthorized,
		Message: "Invalid tenant or password",
	}
	tenant, err := GetTenant(name)
	if err != nil {
		return authenticationError
	}
	err = bcrypt.CompareHashAndPassword([]byte(tenant.Password), []byte(password))
	if err != nil {
		return authenticationError
	}
	if !tenant.IsActive {
		authenticationError.Message = "User deactivated, authorization not alowed."
		return authenticationError
	}
	return nil
}
