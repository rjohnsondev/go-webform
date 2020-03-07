package main

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/BurntSushi/toml"
	// "github.com/davecgh/go-spew/spew"
	_ "github.com/denisenkom/go-mssqldb"
	"github.com/gomarkdown/markdown"
	"github.com/gorilla/mux"
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
	"time"
)

type DBType string

const (
	DbSqlServer = "sqlserver"
	DbPostgres  = "postgres"
)

var db *sql.DB
var dbType DBType
var formTemplate *template.Template
var listTemplate *template.Template

type Form struct {
	Name                     string
	Description              string
	TableName                string
	Fields                   []*FormField
	PreviouslyInsertedRecord string
}

type FormField struct {
	Name             string
	FieldType        FormFieldType
	Options          []string
	Required         bool
	Label            string
	Description      template.HTML
	Regex            template.JSStr
	Placeholder      string
	SectionHeading   string
	LinebreakAfter   bool
	IncludeInSummary bool
}

type FormFieldType string

const (
	FormText      FormFieldType = "text"
	FormVarChar                 = "varchar"
	FormInteger                 = "integer"
	FormDecimal                 = "decimal"
	FormMoney                   = "money"
	FormFloat                   = "float"
	FormBoolean                 = "boolean"
	FormSelect                  = "select"
	FormRadio                   = "radio"
	FormTimeStamp               = "timestamp"
	FormDate                    = "date"
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

func dataTypeToFieldType(dt string) FormFieldType {
	if dbType == DbPostgres {
		switch dt {
		case "character varying":
			return FormVarChar
		case "text":
			return FormText
		case "integer":
			return FormInteger
		case "numeric":
			return FormDecimal
		case "money":
			return FormMoney
		case "double precision":
			return FormFloat
		case "boolean":
			return FormBoolean
		case "timestamp with time zone":
			return FormTimeStamp
		case "date":
			return FormDate
		}
	} else {
		switch dt {
		case "varchar":
			return FormVarChar
		case "text":
			return FormText
		case "int":
			return FormInteger
		case "bit":
			return FormBoolean
		case "datetimeoffset":
			return FormTimeStamp
		}
	}
	return FormVarChar
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
			field.Label = strings.Title(strings.ReplaceAll(col.name, "_", " "))
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

func loadFormList(ctx context.Context, user string, frm *Form) ([]map[string]string, error) {
	cols := make([]string, 0, len(frm.Fields))
	vals := make([]interface{}, 0, len(cols))
	// add the id for the first col
	cols = append(cols, "id")
	var val interface{} = 0
	vals = append(vals, &val)
	// add the rest
	for _, fld := range frm.Fields {
		if fld.IncludeInSummary {
			cols = append(cols, fld.Name)
			val := emptyFormVal(fld.FieldType)
			vals = append(vals, &val)
		}
	}

	query := fmt.Sprintf("SELECT %s FROM %s WHERE created_user = ", strings.Join(cols, ","), frm.TableName)
	if dbType == DbSqlServer {
		query += "@p1 ORDER BY created_ts DESC"
	} else {
		query += "$1 ORDER BY created_ts DESC"
	}
	rows, err := db.QueryContext(ctx, query, user)

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
		cols = append(cols, fld.Name)
		val := emptyFormVal(fld.FieldType)
		vals = append(vals, &val)
	}

	query := fmt.Sprintf("SELECT %s FROM %s WHERE ", strings.Join(cols, ","), frm.TableName)
	if dbType == DbSqlServer {
		query += "id = @p1 AND created_user = @p2"
	} else {
		query += "id = $1 AND created_user = $2"
	}
	err := db.QueryRowContext(ctx, query, id, username).Scan(vals...)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, errors.Wrap(err, "Unable to find record")
		}
		return nil, errors.Wrap(err, "loadFormList query error")
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
		switch field.FieldType {
		case FormInteger:
			if !field.Required && req.FormValue(field.Name) == "" {
				val = nil
			} else {
				val, err = strconv.Atoi(req.FormValue(field.Name))
				if err != nil {
					return 0, errors.Wrap(err, fmt.Sprintf("unable to parse as int %s: %s", field.Name, req.FormValue(field.Name)))
				}
			}
		case FormDecimal:
			if !field.Required && req.FormValue(field.Name) == "" {
				val = nil
			} else {
				val, err = decimal.NewFromString(req.FormValue(field.Name))
				if err != nil {
					return 0, errors.Wrap(err, fmt.Sprintf("unable to parse as decimal %s: %s", field.Name, req.FormValue(field.Name)))
				}
			}
		case FormMoney:
			if !field.Required && req.FormValue(field.Name) == "" {
				val = nil
			} else {
				val, err = decimal.NewFromString(req.FormValue(field.Name))
				if err != nil {
					return 0, errors.Wrap(err, fmt.Sprintf("unable to parse as money %s: %s", field.Name, req.FormValue(field.Name)))
				}
			}
		case FormFloat:
			if !field.Required && req.FormValue(field.Name) == "" {
				val = nil
			} else {
				val, err = strconv.ParseFloat(req.FormValue(field.Name), 64)
				if err != nil {
					return 0, errors.Wrap(err, fmt.Sprintf("unable to parse as float %s: %s", field.Name, req.FormValue(field.Name)))
				}
			}
		// bools can't be not null
		case FormBoolean:
			val = req.FormValue(field.Name) == "1"
		case FormTimeStamp:
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

func emptyFormVal(fieldType FormFieldType) interface{} {
	switch fieldType {
	case FormText:
		return ""
	case FormVarChar:
		return ""
	case FormInteger:
		return int64(0)
	case FormDecimal:
		return decimal.Decimal{}
	case FormMoney:
		return decimal.Decimal{}
	case FormFloat:
		return float64(0)
	case FormBoolean:
		return false
	case FormSelect:
		return ""
	case FormRadio:
		return ""
	case FormTimeStamp:
		return time.Time{}
	case FormDate:
		return time.Time{}
	}
	return ""
}

func formValFromInterface(fieldType FormFieldType, valPtr interface{}) string {
	val := *(valPtr.(*interface{}))

	if val == nil {
		return ""
	}

	switch fieldType {
	case FormText:
		return val.(string)
	case FormVarChar:
		return val.(string)
	case FormInteger:
		return fmt.Sprintf("%v", val.(int64))
	case FormDecimal:
		return fmt.Sprintf("%v", val.(decimal.Decimal))
	case FormMoney:
		return fmt.Sprintf("%v", val.(decimal.Decimal))
	case FormFloat:
		return fmt.Sprintf("%v", val.(float64))
	//case FormBoolean:
	//	return fmt.Sprintf("%v", val.(bool))
	case FormSelect:
		return val.(string)
	case FormRadio:
		return val.(string)
	case FormTimeStamp:
		return fmt.Sprintf("%v", val.(time.Time))
	case FormDate:
		return fmt.Sprintf("%v", val.(time.Time))
	case FormBoolean:
		if val.(bool) {
			return "1"
		} else {
			return ""
		}
	}
	return ""
}

func ServeForm(w http.ResponseWriter, req *http.Request) {
	var err error

	vars := mux.Vars(req)
	formPath, exists := vars["table_name"]
	if !exists {
		http.Error(w, "Check form path", http.StatusNotFound)
		return
	}

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
			return
		}
		err = formTemplate.Execute(w, map[string]interface{}{"frm": frm, "vals": map[string]string{}})
		if err != nil {
			log.Println(err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	} else if req.Method == http.MethodPost {

		creds := goidentity.FromHTTPRequestContext(req)
		username := "anonymous"
		if creds != nil {
			username = creds.UserName()
		}

		insertedId, err := saveFormSubmission(ctx, username, frm, req)
		if err != nil {
			log.Println(err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		cookie := http.Cookie{Name: "inserted", Value: strconv.Itoa(insertedId)}
		http.SetCookie(w, &cookie)
		http.Redirect(w, req, req.URL.Path, 302)

	}
}

func ServeFormListEntries(w http.ResponseWriter, req *http.Request) {
	var err error

	vars := mux.Vars(req)
	formPath, exists := vars["table_name"]
	if !exists {
		http.Error(w, "Check form path", http.StatusNotFound)
		return
	}

	ctx := req.Context()

	var frm *Form
	if frm, err = loadForm(ctx, formPath); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	creds := goidentity.FromHTTPRequestContext(req)
	username := "anonymous"
	if creds != nil {
		username = creds.UserName()
	}

	// we are requesting a list of submissions for this user
	listTemplate, err = template.ParseFiles("list.template.html")
	if err != nil {
		log.Println(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	vals, err := loadFormList(ctx, username, frm)
	if err != nil {
		log.Println(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	err = listTemplate.Execute(w, map[string]interface{}{"frm": frm, "vals": vals})
	if err != nil {
		log.Println(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

func ServeFormEntry(w http.ResponseWriter, req *http.Request) {
	var err error

	vars := mux.Vars(req)
	formPath, exists := vars["table_name"]
	if !exists {
		http.Error(w, "Check form path", http.StatusNotFound)
		return
	}
	entryIdStr, exists := vars["id"]
	entryId, err := strconv.Atoi(entryIdStr)
	if !exists || err != nil {
		http.Error(w, "Check form id path", http.StatusNotFound)
		return
	}

	ctx := req.Context()

	var frm *Form
	if frm, err = loadForm(ctx, formPath); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	creds := goidentity.FromHTTPRequestContext(req)
	username := "anonymous"
	if creds != nil {
		username = creds.UserName()
	}

	// we are requesting a list of submissions for this user
	formTemplate, err = template.ParseFiles("index.template.html")
	if err != nil {
		log.Println(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	vals, err := loadFormEntry(ctx, username, entryId, frm)
	// fmt.Println(vals)
	if err != nil {
		log.Println(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	err = formTemplate.Execute(w, map[string]interface{}{"frm": frm, "vals": vals})
	if err != nil {
		log.Println(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
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

type spnegoMiddleware struct {
	kt         *keytab.Keytab
	sessionKey string
	cookieName string
}

func (sm *spnegoMiddleware) Middleware(next http.Handler) http.Handler {
	l := log.New(os.Stderr, "GOKRB5 Service: ", log.Ldate|log.Ltime|log.Lshortfile)
	return spnego.SPNEGOKRB5Authenticate(
		next,
		sm.kt,
		service.Logger(l),
		service.SessionManager(NewSessionMgr(sm.sessionKey, sm.cookieName)),
	)
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

	r := mux.NewRouter()
	r.PathPrefix("/static/").Handler(
		http.StripPrefix("/static/", http.FileServer(http.Dir(conf.Server.StaticDir))),
	)
	r.HandleFunc("/{table_name}/edit/{id:[0-9]+}", ServeFormEntry)
	r.HandleFunc("/{table_name}/list", ServeFormListEntries)
	r.HandleFunc("/{table_name}", ServeForm)
	r.HandleFunc("/", ServeForm)

	if conf.Auth.Keytab != "" {
		b, err := ioutil.ReadFile(conf.Auth.Keytab)
		if err != nil {
			log.Fatal(err)
		}
		kt := keytab.New()
		if kt.Unmarshal(b) != nil {
			log.Fatal(err)
		}

		sm := spnegoMiddleware{
			kt:         kt,
			sessionKey: conf.Auth.SessionKey,
			cookieName: conf.Auth.CookieName,
		}
		r.Use(sm.Middleware)
	}

	if conf.Server.Certificate != "" {
		log.Print("Starting TLS (https) server, listening on " + conf.Server.Listen)
		err = http.ListenAndServeTLS(conf.Server.Listen, conf.Server.Certificate, conf.Server.Key, r)
	} else {
		log.Print("Starting (http) server, listening on " + conf.Server.Listen)
		err = http.ListenAndServe(conf.Server.Listen, r)
	}
	if err != nil {
		log.Fatal(err)
	}
}
