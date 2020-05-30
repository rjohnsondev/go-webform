package main

import (
	"context"
	"database/sql"
	"fmt"
	_ "github.com/denisenkom/go-mssqldb"
	"github.com/gomarkdown/markdown"
	_ "github.com/lib/pq"
	"github.com/pkg/errors"
	"html/template"
	"log"
	"strconv"
	"strings"
)

type DBType string

const (
	DbSqlServer DBType = "sqlserver"
	DbPostgres         = "postgres"
)

type dbCol struct {
	name    string
	colType string
	notNull bool
}

var db *sql.DB
var dbType DBType

func loadTableDBCols(ctx context.Context, tableName string) ([]*dbCol, error) {

	query := `
		SELECT f.attname,
			   pg_catalog.format_type(f.atttypid, f.atttypmod),
       		   f.attnotnull
		FROM
			pg_attribute f
			JOIN pg_class c ON c.oid = f.attrelid
			LEFT JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE c.relkind = 'r'::char
		  AND n.nspname = 'public'
		  AND c.relname = $1
		  AND f.attnum > $2
		ORDER BY f.attnum
		`
	if dbType == DbSqlServer {
		query = `
			SELECT COLUMN_NAME,
				   DATA_TYPE,
				   IIF(IS_NULLABLE = 'NO', 1, 0)
			FROM information_schema.columns
			WHERE table_name = @p1
			  AND ORDINAL_POSITION > @p2
			ORDER BY ordinal_position;
			`
	}

	// we skip the top 4 rows to just get to the content (f.attnum > 4)
	rows, err := db.QueryContext(ctx, query, tableName, 4)
	if err != nil {
		return nil, errors.Wrap(err, "unable to query table metadata")
	}

	cols := make([]*dbCol, 0)
	for rows.Next() {
		col := dbCol{}
		err = rows.Scan(&col.name, &col.colType, &col.notNull)
		if err != nil {
			return nil, errors.Wrap(err, "unable to read table column metadata")
		}
		cols = append(cols, &col)
	}
	if closeErr := rows.Close(); closeErr != nil {
		return nil, errors.Wrap(err, "unable to close column metadata rows")
	}
	return cols, nil
}

func loadField(ctx context.Context, col *dbCol, tableName string) (*FormField, error) {
	fieldType := dataTypeToFieldType(col.colType)

	field := &FormField{
		Name:      col.name,
		FieldType: fieldType,
		Required:  col.notNull,
	}

	labelsTable := tableName + "_labels"
	query := `
		SELECT
			label,
			description,
			placeholder,
			options,
			options_as_radio,
			section_heading,
			linebreak_after,
			include_in_summary
		FROM ` + labelsTable + " WHERE column_name = $1"
	if dbType == DbSqlServer {
		query = strings.ReplaceAll(query, "$1", "@p1")
	}
	options := ""
	optionsAsRadio := false
	err :=
		db.
			QueryRowContext(ctx, query, col.name).
			Scan(
				&field.Label,
				&field.Description,
				&field.Placeholder,
				&options,
				&optionsAsRadio,
				&field.SectionHeading,
				&field.LinebreakAfter,
				&field.IncludeInSummary)
	if err != nil {
		if err == sql.ErrNoRows {
			// we had no label metadata for this field, that's cool, just give it something default
			field.Label = strings.ReplaceAll(col.name, "_", " ")
			if len(field.Label) > 0 {
				field.Label = strings.ToUpper(field.Label[0:1]) + field.Label[1:]
			}
		} else {
			return nil, errors.Wrap(err, fmt.Sprintf("query error, does the table %s exist?", labelsTable))
		}
	}

	if options != "" {
		if optionsAsRadio {
			field.FieldType = FormRadio
		} else {
			field.FieldType = FormSelect
		}
		field.Options = strings.Split(options, ",")
	}

	if field.Description != "" {
		field.Description = template.HTML(markdown.ToHTML([]byte(field.Description), nil, nil))
	}

	return field, nil
}

func loadForm(ctx context.Context, formPath string) (*Form, error) {
	// let's get the other details for the form
	form := new(Form)
	query := "SELECT name, description, table_name, admins, allow_anonymous, use_ldap_fields FROM forms WHERE path = $1"
	if dbType == DbSqlServer {
		query = "SELECT name, description, table_name, admins, allow_anonymous, use_ldap_fields FROM forms WHERE path = @p1"
	}
	admins := ""
	err :=
		db.
			QueryRowContext(ctx, query, formPath).
			Scan(&form.Name, &form.Description, &form.TableName, &admins, &form.AllowAnonymous, &form.UseLDAPFields)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, errors.Errorf("no form with path %s", formPath)
		}
		return nil, errors.Wrap(err, "loadForm query error")
	}
	form.Admins = make(map[string]bool)
	for _, f := range strings.Split(admins, ",") {
		form.Admins[strings.TrimSpace(f)] = true
	}

	dbCols, err := loadTableDBCols(ctx, form.TableName)
	if err != nil {
		return nil, errors.Wrap(err, "query error")
	}

	if ldapConn == nil {
		form.UseLDAPFields = false
	}

	fields := make([]*FormField, 0, len(dbCols))
	for _, col := range dbCols {
		field, err := loadField(ctx, col, form.TableName)
		if err != nil {
			return nil, err
		}
		field.IsLDAPPopulated = form.UseLDAPFields && isLDAPField(field.Name)
		fields = append(fields, field)
	}
	form.Fields = fields

	return form, nil
}

func isLDAPField(fieldName string) bool {
	return fieldName == "user_employee_number" ||
		fieldName == "user_display_name" ||
		fieldName == "user_department" ||
		fieldName == "user_email" ||
		fieldName == "user_location" ||
		fieldName == "manager" ||
		fieldName == "manager_employee_number" ||
		fieldName == "manager_display_name" ||
		fieldName == "manager_department" ||
		fieldName == "manager_email" ||
		fieldName == "manager_location"
}

func loadFormList(ctx context.Context, user string, frm *Form) ([]map[string]string, error) {

	cols := make([]string, 0, len(frm.Fields))
	vals := make([]interface{}, 0, len(cols))
	// add the id for the first col
	cols = append(cols, "id")
	var valId interface{} = 0
	vals = append(vals, &valId)
	// created user
	cols = append(cols, "created_user")
	var valUsr interface{} = ""
	vals = append(vals, &valUsr)
	// created ts
	cols = append(cols, "created_ts")
	var valTs interface{} = ""
	vals = append(vals, &valTs)
	// add the rest
	for _, fld := range frm.Fields {
		if fld.IncludeInSummary {
			cols = append(cols, fld.Name)
			val := emptyFormVal(fld.FieldType)
			vals = append(vals, &val)
		}
	}

	isAdmin, _ := frm.Admins[user]
	var rows *sql.Rows
	var err error
	if isAdmin {
		query := fmt.Sprintf("SELECT %s FROM %s ORDER BY created_ts DESC", strings.Join(cols, ","), frm.TableName)
		rows, err = db.QueryContext(ctx, query)
	} else {
		query := fmt.Sprintf("SELECT %s FROM %s WHERE created_user = ", strings.Join(cols, ","), frm.TableName)
		if dbType == DbSqlServer {
			query += "@p1 ORDER BY created_ts DESC"
		} else {
			query += "$1 ORDER BY created_ts DESC"
		}
		rows, err = db.QueryContext(ctx, query, user)
	}

	if err != nil {
		if err == sql.ErrNoRows {
			return make([]map[string]string, 0), nil
		}
		return nil, errors.Wrap(err, "loadFormList query error")
	}

	out := make([]map[string]string, 0)
	for rows.Next() {
		err = rows.Scan(vals...)
		if err != nil {
			return nil, errors.Wrap(err, "unable to read table values")
		}
		outRow := make(map[string]string)
		i := 0
		// get the id out first
		outRow["id"] = formValFromInterface(FormInteger, vals[i])
		i++
		outRow["created_user"] = formValFromInterface(FormVarChar, vals[i])
		i++
		outRow["created_ts"] = formValFromInterface(FormTimeStamp, vals[i])
		i++
		// now the rest
		for _, fld := range frm.Fields {
			if fld.IncludeInSummary {
				outRow[fld.Name] = formValFromInterface(fld.FieldType, vals[i])
				i++
			}
		}
		out = append(out, outRow)
	}
	if closeErr := rows.Close(); closeErr != nil {
		return nil, errors.Wrap(err, "unable to close rows for table values")
	}

	return out, nil
}

func loadFormEntry(ctx context.Context, username string, id int, frm *Form) (map[string]string, error) {
	cols := make([]string, 0, len(frm.Fields))
	vals := make([]interface{}, 0, len(cols))
	for _, fld := range frm.Fields {
		if fld.FieldType == FormMoney && dbType == DbPostgres {
			cols = append(cols, fld.Name+"::numeric")
		} else {
			cols = append(cols, fld.Name)
		}
		val := emptyFormVal(fld.FieldType)
		vals = append(vals, &val)
	}

	isAdmin, _ := frm.Admins[username]

	query := fmt.Sprintf("SELECT %s FROM %s WHERE ", strings.Join(cols, ","), frm.TableName)
	var err error
	if isAdmin {
		if dbType == DbSqlServer {
			query += "id = @p1"
		} else {
			query += "id = $1"
		}
		err = db.QueryRowContext(ctx, query, id).Scan(vals...)
	} else {
		if dbType == DbSqlServer {
			query += "id = @p1 AND created_user = @p2"
		} else {
			query += "id = $1 AND created_user = $2"
		}
		err = db.QueryRowContext(ctx, query, id, username).Scan(vals...)
	}

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, errors.Wrap(err, "Unable to find record")
		}
		return nil, errors.Wrap(err, "loadFormEntry query error")
	}

	outRow := make(map[string]string)
	i := 0
	for _, fld := range frm.Fields {
		outRow[fld.Name] = formValFromInterface(fld.FieldType, vals[i])
		i++
	}

	return outRow, nil
}

func generateInsertStatement(tableName string, fields []*FormField) string {
	fieldNames := make([]string, 0, len(fields))
	for _, field := range fields {
		fieldNames = append(fieldNames, field.Name)
	}
	query := ""
	if dbType == DbPostgres {
		placeholders := make([]string, 0, len(fields))
		for i, _ := range fields {
			placeholders = append(placeholders, fmt.Sprintf("$%d", i+2))
		}
		query = fmt.Sprintf(
			`INSERT INTO %s
				(created_ts, updated_ts, created_user, %s)
				VALUES (CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, $1, %s) RETURNING id`,
			tableName,
			strings.Join(fieldNames, ","),
			strings.Join(placeholders, ","))
	} else {
		placeholders := make([]string, 0, len(fields))
		for i, _ := range fields {
			placeholders = append(placeholders, fmt.Sprintf("@p%d", i+2))
		}
		query = fmt.Sprintf(
			`INSERT INTO %s
				(created_ts, updated_ts, created_user, %s) OUTPUT INSERTED.id
				VALUES (CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, @p1, %s)`,
			tableName,
			strings.Join(fieldNames, ","),
			strings.Join(placeholders, ","))
	}
	return query
}

func generateUpdateStatement(tableName string, isAdmin bool, fields []*FormField) string {
	fieldNames := make([]string, 0, len(fields))
	for _, field := range fields {
		// ldap fields cannot be updated.
		if !field.IsLDAPPopulated {
			fieldNames = append(fieldNames, field.Name)
		}
	}
	query := ""
	if dbType == DbPostgres {
		placeholders := ""
		for i, field := range fields {
			if !field.IsLDAPPopulated {
				placeholders = fmt.Sprintf("%s, %s = $%d", placeholders, field.Name, i+3)
			}
		}
		if isAdmin {
			query = fmt.Sprintf(
				`UPDATE %s SET updated_ts = CURRENT_TIMESTAMP %s WHERE id = $1 and $2 <> '' RETURNING id`,
				tableName,
				placeholders)
		} else {
			query = fmt.Sprintf(
				`UPDATE %s SET updated_ts = CURRENT_TIMESTAMP %s WHERE id = $1 and created_user = $2 RETURNING id`,
				tableName,
				placeholders)
		}
	} else {
		placeholders := ""
		for i, field := range fields {
			if !field.IsLDAPPopulated {
				placeholders = fmt.Sprintf("%s, %s = @p%d", placeholders, field.Name, i+3)
			}
		}
		if isAdmin {
			query = fmt.Sprintf(
				`UPDATE %s SET updated_ts = CURRENT_TIMESTAMP %s OUTPUT INSERTED.id WHERE id = @p1 AND @p2 <> ''`,
				tableName,
				placeholders)
		} else {
			query = fmt.Sprintf(
				`UPDATE %s SET updated_ts = CURRENT_TIMESTAMP %s OUTPUT INSERTED.id WHERE id = @p1 AND created_user = @p2`,
				tableName,
				placeholders)
		}
	}
	return query
}

func minOffsetToTZOffset(minsStr string) string {
	tzOffset, err := strconv.Atoi(minsStr)
	if err != nil {
		log.Printf("error parsing time-offset, attempting to save with UTC TZ: %s\n", err)
		return "+00:00"
	}
	tzOffset = tzOffset * -1
	hrs := tzOffset / 60
	mins := tzOffset % 60
	out := fmt.Sprintf("%02d:%02d", hrs, mins)
	if tzOffset >= 0 {
		return "+" + out
	}
	return "-" + out
}

func connectToDb(conf tomlConfig) {
	var err error

	// connect to the database
	dbType = conf.Database.DbType
	db, err = sql.Open(string(dbType), conf.Database.ConnectionString)
	if err != nil {
		log.Fatal(err)
	}

}
