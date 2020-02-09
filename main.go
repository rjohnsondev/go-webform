package main

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/gomarkdown/markdown"
	_ "github.com/lib/pq"
	"github.com/pkg/errors"
	"html/template"
	"log"
	"net/http"
	"strconv"
	"strings"
)

// TODO: add https://github.com/korylprince/go-ad-auth

var db *sql.DB
var formTemplate *template.Template

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

	return out, nil
}

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
	DateTime                  = "datetime-local"
)

type dbCol struct {
	name           string
	colType        string
	notNull        bool
	lookupTable    string
	lookupTableCol string
}

func loadTableDBCols(ctx context.Context, tableName string) ([]*dbCol, error) {
	query := `
		SELECT f.attname,
			   pg_catalog.format_type(f.atttypid, f.atttypmod),
       		   f.attnotnull,
			   COALESCE(g.relname, ''),
			   COALESCE(f2.attname, '')
		FROM pg_attribute f
				 JOIN pg_class c ON c.oid = f.attrelid
				 LEFT JOIN pg_namespace n ON n.oid = c.relnamespace
				 LEFT JOIN pg_constraint p ON p.conrelid = c.oid AND f.attnum = ANY (p.conkey)
				 LEFT JOIN pg_class AS g ON p.confrelid = g.oid
				 LEFT JOIN pg_attribute AS f2 ON p.confrelid = f2.attrelid and f2.attnum > 0
		WHERE c.relkind = 'r'::char
		  AND n.nspname = 'public'
		  AND c.relname = $1
		  AND f.attnum > $2
		ORDER BY f.attnum
		`

	// we skip the top 3 rows to just get to the content (f.attnum > 3)
	rows, err := db.QueryContext(ctx, query, tableName, 3)
	if err != nil {
		return nil, errors.Wrap(err, "unable to query table metadata")
	}

	cols := make([]*dbCol, 0)
	for rows.Next() {
		col := dbCol{}
		err = rows.Scan(&col.name, &col.colType, &col.notNull, &col.lookupTable, &col.lookupTableCol)
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

func loadFieldOptions(ctx context.Context, lookupTable string, lookupTableCol string) ([]string, error) {
	query := "SELECT " + lookupTableCol + " FROM " + lookupTable
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil,
			errors.Wrap(
				err,
				fmt.Sprintf(
					"unable to query lookup %s.%s",
					lookupTable,
					lookupTableCol))
	}
	fieldOptions := make([]string, 0)
	for rows.Next() {
		val := ""
		err = rows.Scan(&val)
		if err != nil {
			return nil, errors.Wrap(err, "unable to read column lookup val")
		}
		fieldOptions = append(fieldOptions, val)
	}
	if closeErr := rows.Close(); closeErr != nil {
		return nil, errors.Wrap(err, "unable to close column lookup rows")
	}
	return fieldOptions, nil
}

func loadForm(ctx context.Context, formPath string) (*Form, error) {
	// let's get the other details for the form
	form := new(Form)
	err :=
		db.
			QueryRowContext(ctx, "SELECT name, description, table_name FROM forms WHERE path = $1", formPath).
			Scan(&form.Name, &form.Description, &form.TableName)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, errors.Errorf("no form with path %s", formPath)
		}
		return nil, errors.Wrap(err, "query error")
	}

	dbCols, err := loadTableDBCols(ctx, form.TableName)
	if err != nil {
		return nil, errors.Wrap(err, "query error")
	}

	fields := make([]*FormField, 0, len(dbCols))
	for _, col := range dbCols {
		fieldType := TextField
		switch col.colType {
		case "character varying":
			fieldType = TextField
		case "text":
			fieldType = TextArea
		case "integer":
			fieldType = NumberField
		case "boolean":
			fieldType = Checkbox
		case "timestamp with time zone":
			fieldType = DateTime
		}

		fieldOptions := make([]string, 0)
		if col.lookupTable != "" {
			fieldType = DropDown
			fieldOptions, err = loadFieldOptions(ctx, col.lookupTable, col.lookupTableCol)
			if err != nil {
				return nil, errors.Wrap(err, fmt.Sprintf("unable to get lookup for table %s", col.name))
			}
		}

		field := &FormField{
			Name:     col.name,
			Type:     fieldType,
			Options:  fieldOptions,
			Required: col.notNull,
		}

		labelsTable := form.TableName + "_labels"
		query := "SELECT label, description, placeholder, section_heading, linebreak_after FROM " +
			labelsTable + " WHERE column_name = $1"
		err :=
			db.
				QueryRowContext(ctx, query, col.name).
				Scan(&field.Label, &field.Description, &field.Placeholder, &field.SectionHeading, &field.LinebreakAfter)
		if err != nil {
			if err == sql.ErrNoRows {
				// we had no label metadata for this field, that's cool, just give it something default
				field.Label = strings.Title(strings.ReplaceAll(col.name, "_", " "))
			} else {
				return nil, errors.Wrap(err, fmt.Sprintf("query error, does the table %s exist?", labelsTable))
			}
		}

		if field.Description != "" {
			field.Description = template.HTML(markdown.ToHTML([]byte(field.Description), nil, nil))
		}

		// TODO: description should be markdown rendered with https://github.com/gomarkdown/markdown

		fields = append(fields, field)
	}
	form.Fields = fields

	return form, nil
}

func saveFormSubmission(ctx context.Context, frm *Form, req *http.Request) (int, error) {
	fieldNames := make([]string, 0, len(frm.Fields))
	for _, field := range frm.Fields {
		fieldNames = append(fieldNames, field.Name)
	}
	placeholders := make([]string, 0, len(frm.Fields))
	for i, _ := range frm.Fields {
		placeholders = append(placeholders, fmt.Sprintf("$%d", i+2))
	}
	query := fmt.Sprintf(
		"INSERT INTO %s (created_ts, created_user, %s) VALUES (CURRENT_TIMESTAMP, $1, %s) RETURNING id",
		frm.TableName,
		strings.Join(fieldNames, ","),
		strings.Join(placeholders, ","))

	values := make([]interface{}, 0, len(frm.Fields)+1)
	values = append(values, "")
	for _, field := range frm.Fields {
		var val interface{}
		var err error
		switch field.Type {
		case NumberField:
			val, err = strconv.Atoi(req.FormValue(field.Name))
			if err != nil {
				return 0, errors.Wrap(err, fmt.Sprintf("unable to parse as int %s: %s", field.Name, req.FormValue(field.Name)))
			}
		case Checkbox:
			val = req.FormValue(field.Name) == "1"
		default:
			val = req.FormValue(field.Name)
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

func formHandler(w http.ResponseWriter, req *http.Request) {
	var err error

	// get the form path by stripping off the initial slash.
	formPath := req.URL.Path
	if len(formPath) > 1 {
		formPath = formPath[1:]
	}

	// leverage the context from our request to cancel queries and whatnot if the http client goes away
	ctx := req.Context()

	var frm *Form
	if frm, err = loadForm(ctx, formPath); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
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
		formTemplate, err = template.ParseFiles("index.html")
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
		// http.Error(w, "Post not yet supported", http.StatusMethodNotAllowed)
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

	// connect to the database
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

	// parse the form template
	formTemplate, err = template.ParseFiles("index.html")
	//if err != nil {
	//	log.Fatal(err)
	//}

	// add the paths to the web server
	paths, err := formPaths(db)
	for _, p := range paths {
		http.HandleFunc("/"+p, formHandler)
	}

	// start listening
	log.Print("Starting server")
	err = http.ListenAndServe(":8090", nil)
	if err != nil {
		log.Fatal(err)
	}
}
