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
	"net/http"
	"strconv"
	"strings"
)

type DBType int

const (
	DbSqlServer = iota
	DbPostgres  = iota
)

var db *sql.DB
var dbType DBType
var formTemplate *template.Template

type Form struct {
	Name                     string
	Description              string
	TableName                string
	Fields                   []*FormField
	PreviouslyInsertedRecord string
}

type FormField struct {
	Name           string
	Type           FormFieldType
	Options        []string
	Required       bool
	Label          string
	Description    template.HTML
	Placeholder    string
	SectionHeading string
	LinebreakAfter bool
}

type FormFieldType string

const (
	TextField   FormFieldType = "text"
	NumberField               = "number"
	TextArea                  = "textarea"
	Checkbox                  = "checkbox"
	DropDown                  = "select"
	Radio                     = "radio"
	DateTime                  = "datetime-local"
)

type dbCol struct {
	name    string
	colType string
	notNull bool
}

func formPaths(db *sql.DB) ([]string, error) {
	rows, err := db.Query("SELECT path FROM forms")
	if err != nil {
		return nil, errors.Wrap(err, "unable to get form paths")
	}
	out := make([]string, 0)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, errors.Wrap(err, "unable to load form path")
		}
		out = append(out, name)
	}
	// Check for errors from iterating over rows.
	if err := rows.Err(); err != nil {
		log.Fatal(err)
	}
	if closeErr := rows.Close(); closeErr != nil {
		return nil, errors.Wrap(err, "unable to close form rows")
	}
	return out, nil
}

func loadTableDBCols(ctx context.Context, tableName string) ([]*dbCol, error) {

	query := `
		SELECT f.attname,
			   pg_catalog.format_type(f.atttypid, f.atttypmod),
       		   f.attnotnull
		FROM pg_attribute f
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

	// we skip the top 3 rows to just get to the content (f.attnum > 3)
	rows, err := db.QueryContext(ctx, query, tableName, 3)
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

func dataTypeToFieldType(dt string) FormFieldType {
	if dbType == DbPostgres {
		switch dt {
		case "character varying":
			return TextField
		case "text":
			return TextArea
		case "integer":
			return NumberField
		case "boolean":
			return Checkbox
		case "timestamp with time zone":
			return DateTime
		}
	} else {
		switch dt {
		case "varchar":
			return TextField
		case "text":
			return TextArea
		case "int":
			return NumberField
		case "bit":
			return Checkbox
		case "datetimeoffset":
			return DateTime
		}
	}
	return TextField
}

func loadField(ctx context.Context, col *dbCol, tableName string) (*FormField, error) {
	fieldType := dataTypeToFieldType(col.colType)

	field := &FormField{
		Name:     col.name,
		Type:     fieldType,
		Required: col.notNull,
	}

	labelsTable := tableName + "_labels"
	query := "SELECT label, description, placeholder, options, options_as_radio, section_heading, linebreak_after FROM " +
		labelsTable + " WHERE column_name = $1"
	if dbType == DbSqlServer {
		query = strings.ReplaceAll(query, "$1", "@p1")
	}
	options := ""
	optionsAsRadio := false
	err :=
		db.
			QueryRowContext(ctx, query, col.name).
			Scan(&field.Label, &field.Description, &field.Placeholder, &options, &optionsAsRadio, &field.SectionHeading, &field.LinebreakAfter)
	if err != nil {
		if err == sql.ErrNoRows {
			// we had no label metadata for this field, that's cool, just give it something default
			field.Label = strings.Title(strings.ReplaceAll(col.name, "_", " "))
		} else {
			return nil, errors.Wrap(err, fmt.Sprintf("query error, does the table %s exist?", labelsTable))
		}
	}

	if options != "" {
		if optionsAsRadio {
			field.Type = Radio
		} else {
			field.Type = DropDown
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
	query := "SELECT name, description, table_name FROM forms WHERE path = $1"
	if dbType == DbSqlServer {
		query = "SELECT name, description, table_name FROM forms WHERE path = @p1"
	}
	err :=
		db.
			QueryRowContext(ctx, query, formPath).
			Scan(&form.Name, &form.Description, &form.TableName)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, errors.Errorf("no form with path %s", formPath)
		}
		return nil, errors.Wrap(err, "loadForm query error")
	}

	dbCols, err := loadTableDBCols(ctx, form.TableName)
	if err != nil {
		return nil, errors.Wrap(err, "query error")
	}

	fields := make([]*FormField, 0, len(dbCols))
	for _, col := range dbCols {
		field, err := loadField(ctx, col, form.TableName)
		if err != nil {
			return nil, err
		}
		fields = append(fields, field)
	}
	form.Fields = fields

	return form, nil
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
			"INSERT INTO %s (created_ts, created_user, %s) VALUES (CURRENT_TIMESTAMP, $1, %s) RETURNING id",
			tableName,
			strings.Join(fieldNames, ","),
			strings.Join(placeholders, ","))
	} else {
		placeholders := make([]string, 0, len(fields))
		for i, _ := range fields {
			placeholders = append(placeholders, fmt.Sprintf("@p%d", i+2))
		}
		query = fmt.Sprintf(
			"INSERT INTO %s (created_ts, created_user, %s) OUTPUT INSERTED.id VALUES (CURRENT_TIMESTAMP, @p1, %s)",
			tableName,
			strings.Join(fieldNames, ","),
			strings.Join(placeholders, ","))
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
	out := fmt.Sprintf("%2d:%2d", hrs, mins)
	if tzOffset >= 0 {
		return "+" + out
	}
	return "-" + out
}

func saveFormSubmission(ctx context.Context, frm *Form, req *http.Request) (int, error) {
	query := generateInsertStatement(frm.TableName, frm.Fields)

	values := make([]interface{}, 0, len(frm.Fields)+1)
	values = append(values, "username goes here!")
	for _, field := range frm.Fields {
		var val interface{}
		var err error
		switch field.Type {
		case NumberField:
			if !field.Required && req.FormValue(field.Name) == "" {
				val = nil
			} else {
				val, err = strconv.Atoi(req.FormValue(field.Name))
				if err != nil {
					return 0, errors.Wrap(err, fmt.Sprintf("unable to parse as int %s: %s", field.Name, req.FormValue(field.Name)))
				}
			}
		case Checkbox:
			val = req.FormValue(field.Name) == "1"
		case DateTime:
			if !field.Required && req.FormValue(field.Name) == "" {
				val = nil
			} else {
				tzOffset := minOffsetToTZOffset(req.FormValue("timezone-offset"))
				val = strings.ReplaceAll(req.FormValue(field.Name), "T", " ") + tzOffset
			}
		default:
			if !field.Required && req.FormValue(field.Name) == "" {
				val = nil
			} else {
				val = req.FormValue(field.Name)
			}
		}
		values = append(values, val)
	}
	insertId := 0
	err := db.QueryRowContext(ctx, query, values...).Scan(&insertId)
	if err != nil {
		return 0, err
	}
	return insertId, nil
}

type handler struct {
	staticHandler http.Handler
}

func (h *handler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	var err error

	// get the form path by stripping off the initial slash.
	formPath := req.URL.Path
	if strings.HasPrefix(formPath, "/static/") {
		http.StripPrefix("/static/", h.staticHandler).ServeHTTP(w, req)
		return
	}
	if len(formPath) > 1 {
		formPath = formPath[1:]
	}

	// leverage the context from our request to cancel queries and whatnot if the http client goes away
	ctx := req.Context()

	var frm *Form
	if frm, err = loadForm(ctx, formPath); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	insertedCookie, err := req.Cookie("inserted")
	if err == nil && insertedCookie.Value != "" {
		frm.PreviouslyInsertedRecord = insertedCookie.Value
		cookie := http.Cookie{Name: "inserted", Value: "", MaxAge: -1}
		http.SetCookie(w, &cookie)
	}

	if req.Method == http.MethodGet {
		// TODO: this is in every request for development purposes, remove when done.
		formTemplate, err = template.ParseFiles("index.template.html")
		if err != nil {
			log.Println(err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
		} else {
			err = formTemplate.Execute(w, frm)
			if err != nil {
				log.Println(err)
				http.Error(w, err.Error(), http.StatusInternalServerError)
			}
		}
	} else if req.Method == http.MethodPost {
		insertedId, err := saveFormSubmission(ctx, frm, req)
		if err != nil {
			log.Println(err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		cookie := http.Cookie{Name: "inserted", Value: strconv.Itoa(insertedId)}
		http.SetCookie(w, &cookie)
		http.Redirect(w, req, req.URL.Path, 302)
	}

}

func main() {
	var err error

	dbType = DbSqlServer

	// connect to the database
	if dbType == DbSqlServer {
		connStr := "sqlserver://sqlserver:gDqDKNnoBhoPzhpk@35.189.5.107?database=forms"
		db, err = sql.Open("sqlserver", connStr)
		if err != nil {
			log.Fatal(err)
		}
		// close db gracefully on shutdown
		defer func() {
			err := db.Close()
			if err != nil {
				log.Fatal(err)
			}
		}()
	} else {
		connStr := "user=richard password=richard dbname=forms"
		db, err = sql.Open("postgres", connStr)
		if err != nil {
			log.Fatal(err)
		}
		// close db gracefully on shutdown
		defer func() {
			err := db.Close()
			if err != nil {
				log.Fatal(err)
			}
		}()
	}

	// parse the form template
	formTemplate, err = template.ParseFiles("index.template.html")
	if err != nil {
		log.Fatal(err)
	}

	// start listening
	log.Print("Starting server")
	h := &handler{
		staticHandler: http.FileServer(http.Dir("static")),
	}
	err = http.ListenAndServe(":8090", h)
	if err != nil {
		log.Fatal(err)
	}
}
