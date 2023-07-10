package db

import (
	"database/sql"
	"time"

	_ "github.com/jackc/pgx/v4/stdlib"
	"github.com/zebrunner/esg/config"
)

type TaskDefinition struct {
	Family                 string    `db:"task_family"`
	Schema                 string    `db:"schema"`
	RegisterDefinitionHash string    `db:"register_definition_hash"`
	RevisionTag            int64     `db:"revision_tag"`
	UpdatedAt              time.Time `db:"updated_at"`
	OverrideDefinitionHash string    `db:"override_definition_hash"`
}

func CreateDefinition(td *TaskDefinition) error {
	tx, err := config.DbConnection.Beginx()
	if err != nil {
		return err
	}

	getFamilyIdQuery := `SELECT family_id FROM families WHERE families.task_family = $1`
	familyId := -1
	err = tx.Get(&familyId, getFamilyIdQuery, td.Family)
	if err != nil {
		if err == sql.ErrNoRows {
			creatFamilyQuery := `INSERT INTO families (task_family) VALUES ($1) RETURNING family_id`
			err = tx.Get(&familyId, creatFamilyQuery, td.Family)
			if err != nil {
				return err
			}
		} else {
			return err
		}
	}

	getSchemaIdQuery := `SELECT schema_id FROM schemas WHERE schemas.schema = $1`
	schemaId := -1
	err = tx.Get(&schemaId, getSchemaIdQuery, td.Schema)
	if err != nil {
		if err == sql.ErrNoRows {
			creatSchemaQuery := `INSERT INTO schemas (schema) VALUES ($1) RETURNING schema_id`
			err = tx.Get(&schemaId, creatSchemaQuery, td.Schema)
			if err != nil {
				return err
			}
		} else {
			return err
		}
	}

	getRelationsQuery := `SELECT count(*) FROM familiesSchemas fs WHERE fs.family_id = $1 AND fs.schema_id = $2`
	count := -1
	err = tx.Get(&count, getRelationsQuery, familyId, schemaId)
	if err != nil {
		return err
	}
	if count == 0 {
		creatRelationsQuery := `INSERT INTO familiesSchemas (family_id, schema_id) VALUES ($1, $2)`
		_, err = tx.Exec(creatRelationsQuery, familyId, schemaId)
		if err != nil {
			return err
		}
	}

	createQuery := `INSERT INTO definitions (register_definition_hash, revision_tag, updated_at, override_definition_hash, schema_id) VALUES ($1, $2, $3, $4, $5)`
	_, err = tx.Exec(createQuery, td.RegisterDefinitionHash, td.RevisionTag, time.Now(), td.OverrideDefinitionHash, schemaId)
	if err != nil {
		return err
	}

	err = tx.Commit()
	if err != nil {
		return err
	}

	return nil
}

func GetDefinition(family string, schema string) (*TaskDefinition, error) {
	getDefinitionQuery := `SELECT f.task_family, s.schema, d.register_definition_hash, d.revision_tag, d.updated_at, d.override_definition_hash
		FROM families f
		INNER JOIN familiesSchemas fs ON f.family_id = fs.family_id
		INNER JOIN schemas s ON fs.schema_id = s.schema_id
		INNER JOIN definitions d ON s.schema_id = d.schema_id
		WHERE f.task_family = $1 AND s.schema = $2
	`

	td := &TaskDefinition{}
	err := config.DbConnection.Get(td, getDefinitionQuery, family, schema)

	if err != nil {
		return nil, err
	}

	return td, nil
}

func RefreshTag(registerHashToAlter string, newTd *TaskDefinition) error {
	updateQuery := `UPDATE definitions
		SET register_definition_hash = $1,
		revision_tag = $2,
		updated_at = $3,
		override_definition_hash = $4
		WHERE register_definition_hash = $5
	`
	_, err := config.DbConnection.Exec(updateQuery, newTd.RegisterDefinitionHash, newTd.RevisionTag, time.Now(), newTd.OverrideDefinitionHash, registerHashToAlter)
	if err != nil {
		return err
	}

	return nil
}
