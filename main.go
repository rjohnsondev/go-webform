package main

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/BurntSushi/toml"
	_ "github.com/denisenkom/go-mssqldb"
	"github.com/gomarkdown/markdown"
	"github.com/gorilla/sessions"
	"github.com/jcmturner/goidentity/v6"
	"github.com/jcmturner/gokrb5/v8/keytab"
	"github.com/jcmturner/gokrb5/v8/service"
	"github.com/jcmturner/gokrb5/v8/spnego"
	_ "github.com/lib/pq"
	"github.com/pkg/errors"
	"github.com/shopspring/decimal"
	"html/template"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
)

type DBType string

const (
	DbSqlServer = "sqlserver"
	DbPostgres  = "postgres"
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
	FormFieldType  FormFieldType
	DBFieldType    DBFieldType
	Options        []string
	Required       bool
	Label          string
	Description    template.HTML
	Regex          template.JSStr
	Placeholder    string
	SectionHeading string
	LinebreakAfter bool
}

type FormFieldType string

const (
	FormTextField   FormFieldType = "text"
	FormNumberField               = "number"
	FormTextArea                  = "textarea"
	FormCheckbox                  = "checkbox"
	FormDropDown                  = "select"
	FormRadio                     = "radio"
	FormDateTime                  = "datetime-local"
)

type DBFieldType string

const (
	DBText      DBFieldType = "text"
	DBVarChar               = "varchar"
	DBInteger               = "integer"
	DBDecimal               = "decimal"
	DBMoney                 = "money"
	DBFloat                 = "float"
	DBBoolean               = "boolean"
	DBTimeStamp             = "timestamp"
)

type dbCol struct {
	name    string
	colType string
	notNull bool
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

func dataTypeToFieldType(dt string) (FormFieldType, DBFieldType) {
	if dbType == DbPostgres {
		switch dt {
		case "character varying":
			return FormTextField, DBVarChar
		case "text":
			return FormTextArea, DBText
		case "integer":
			return FormNumberField, DBInteger
		case "numeric":
			return FormNumberField, DBDecimal
		case "money":
			return FormNumberField, DBMoney
		case "double precision":
			return FormNumberField, DBFloat
		case "boolean":
			return FormCheckbox, DBBoolean
		case "timestamp with time zone":
			return FormDateTime, DBTimeStamp
		}
	} else {
		switch dt {
		case "varchar":
			return FormTextField, DBVarChar
		case "text":
			return FormTextArea, DBText
		case "int":
			return FormNumberField, DBInteger
		case "bit":
			return FormCheckbox, DBBoolean
		case "datetimeoffset":
			return FormDateTime, DBTimeStamp
		}
	}
	return FormTextField, DBVarChar
}

func loadField(ctx context.Context, col *dbCol, tableName string) (*FormField, error) {
	fieldType, dbFieldType := dataTypeToFieldType(col.colType)

	field := &FormField{
		Name:          col.name,
		FormFieldType: fieldType,
		DBFieldType:   dbFieldType,
		Required:      col.notNull,
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
			field.FormFieldType = FormRadio
		} else {
			field.FormFieldType = FormDropDown
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
	out := fmt.Sprintf("%02d:%02d", hrs, mins)
	if tzOffset >= 0 {
		return "+" + out
	}
	return "-" + out
}

func saveFormSubmission(ctx context.Context, username string, frm *Form, req *http.Request) (int, error) {
	query := generateInsertStatement(frm.TableName, frm.Fields)

	values := make([]interface{}, 0, len(frm.Fields)+1)
	values = append(values, username)
	for _, field := range frm.Fields {
		var val interface{}
		var err error
		switch field.DBFieldType {
		case DBInteger:
			if !field.Required && req.FormValue(field.Name) == "" {
				val = nil
			} else {
				val, err = strconv.Atoi(req.FormValue(field.Name))
				if err != nil {
					return 0, errors.Wrap(err, fmt.Sprintf("unable to parse as int %s: %s", field.Name, req.FormValue(field.Name)))
				}
			}
		case DBDecimal:
			if !field.Required && req.FormValue(field.Name) == "" {
				val = nil
			} else {
				val, err = decimal.NewFromString(req.FormValue(field.Name))
				if err != nil {
					return 0, errors.Wrap(err, fmt.Sprintf("unable to parse as decimal %s: %s", field.Name, req.FormValue(field.Name)))
				}
			}
		case DBMoney:
			if !field.Required && req.FormValue(field.Name) == "" {
				val = nil
			} else {
				val, err = decimal.NewFromString(req.FormValue(field.Name))
				if err != nil {
					return 0, errors.Wrap(err, fmt.Sprintf("unable to parse as money %s: %s", field.Name, req.FormValue(field.Name)))
				}
			}
		case DBFloat:
			if !field.Required && req.FormValue(field.Name) == "" {
				val = nil
			} else {
				val, err = strconv.ParseFloat(req.FormValue(field.Name), 64)
				if err != nil {
					return 0, errors.Wrap(err, fmt.Sprintf("unable to parse as float %s: %s", field.Name, req.FormValue(field.Name)))
				}
			}
		case DBBoolean:
			// bools can't be not null
			val = req.FormValue(field.Name) == "1"
		case DBTimeStamp:
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
	useSPNEGO     bool
}

func (h *handler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	var err error

	username := "anonymous"
	if h.useSPNEGO {
		creds := goidentity.FromHTTPRequestContext(req)
		username = creds.UserName()
		//creds.UserName(),
		//creds.Domain(),
		//creds.AuthTime(),
		//creds.SessionID(),
	}

	formPath := req.URL.Path
	if strings.HasPrefix(formPath, "/static/") {
		http.StripPrefix("/static/", h.staticHandler).ServeHTTP(w, req)
		return
	}

	// get the form path by stripping off the initial slash.
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
		insertedId, err := saveFormSubmission(ctx, username, frm, req)
		if err != nil {
			log.Println(err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		cookie := http.Cookie{Name: "inserted", Value: strconv.Itoa(insertedId)}
		http.SetCookie(w, &cookie)
		http.Redirect(w, req, req.URL.Path, 302)
	}

}

type SessionMgr struct {
	skey       []byte
	store      sessions.Store
	cookieName string
}

func NewSessionMgr(sessionKey string, cookieName string) SessionMgr {
	// Best practice is to load this key from a secure location.
	skey := []byte(sessionKey)
	return SessionMgr{
		skey:       skey,
		store:      sessions.NewCookieStore(skey),
		cookieName: cookieName,
	}
}

func (smgr SessionMgr) Get(r *http.Request, k string) ([]byte, error) {
	s, err := smgr.store.Get(r, smgr.cookieName)
	if err != nil {
		return nil, err
	}
	if s == nil {
		return nil, errors.New("nil session")
	}
	b, ok := s.Values[k].([]byte)
	if !ok {
		return nil, fmt.Errorf("could not get bytes held in session at %s", k)
	}
	return b, nil
}

func (smgr SessionMgr) New(w http.ResponseWriter, r *http.Request, k string, v []byte) error {
	s, err := smgr.store.New(r, smgr.cookieName)
	if err != nil {
		return fmt.Errorf("could not get new session from session manager: %v", err)
	}
	s.Values[k] = v
	return s.Save(r, w)
}

type tomlConfig struct {
	Server   serverConfig
	Database databaseConfig
	Auth     authConfig
}

type serverConfig struct {
	Listen      string
	Certificate string
	Key         string
	StaticDir   string
	Template    string
}

type databaseConfig struct {
	DbType           DBType
	ConnectionString string
}

type authConfig struct {
	Keytab     string
	CookieName string
	SessionKey string
}

func main() {
	var err error

	tomlData, err := ioutil.ReadFile("config.toml")
	if err != nil {
		log.Fatalf("unable to load config file: config.toml: %s\n", err)
	}

	var conf tomlConfig
	if _, err := toml.Decode(string(tomlData), &conf); err != nil {
		log.Fatalf("unable to load config file: %s\n", err)
	}

	// connect to the database
	dbType = conf.Database.DbType
	db, err = sql.Open(string(dbType), conf.Database.ConnectionString)
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
	formTemplate, err = template.ParseFiles(conf.Server.Template)
	if err != nil {
		log.Fatal(err)
	}

	// start listening
	h := &handler{
		staticHandler: http.FileServer(http.Dir(conf.Server.StaticDir)),
		useSPNEGO:     conf.Auth.Keytab != "",
	}

	l := log.New(os.Stderr, "GOKRB5 Service: ", log.Ldate|log.Ltime|log.Lshortfile)

	var sh http.Handler = h
	if h.useSPNEGO {
		b, err := ioutil.ReadFile(conf.Auth.Keytab)
		if err != nil {
			log.Fatal(err)
		}
		kt := keytab.New()
		if kt.Unmarshal(b) != nil {
			log.Fatal(err)
		}

		sh = spnego.SPNEGOKRB5Authenticate(h, kt, service.Logger(l),
			service.SessionManager(NewSessionMgr(conf.Auth.SessionKey, conf.Auth.CookieName)))
	}

	if conf.Server.Certificate != "" {
		log.Print("Starting TLS (https) server, listening on " + conf.Server.Listen)
		err = http.ListenAndServeTLS(conf.Server.Listen, conf.Server.Certificate, conf.Server.Key, sh)
	} else {
		log.Print("Starting (http) server, listening on " + conf.Server.Listen)
		err = http.ListenAndServe(conf.Server.Listen, sh)
	}
	if err != nil {
		log.Fatal(err)
	}
}
