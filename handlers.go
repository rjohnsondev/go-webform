package main

import (
	"context"
	"fmt"
	"github.com/gorilla/mux"
	"github.com/jcmturner/goidentity/v6"
	"github.com/pkg/errors"
	"github.com/shopspring/decimal"
	"html/template"
	"log"
	"net/http"
	"strconv"
	"strings"
)

var formTemplate *template.Template
var listTemplate *template.Template

func saveFormSubmission(ctx context.Context, username string, frm *Form, req *http.Request) (int, error) {

	values := make([]interface{}, 0, len(frm.Fields)+1)

	query := ""
	isInsert := req.FormValue("id") == ""
	if isInsert {
		query = generateInsertStatement(frm.TableName, frm.Fields)
		// log.Println(query)
	} else {
		isAdmin, _ := frm.Admins[username]
		query = generateUpdateStatement(frm.TableName, isAdmin, frm.Fields)
		values = append(values, req.FormValue("id"))
		// log.Println("query", query)
	}

	ldapValues := make(map[string]string)
	if frm.UseLDAPFields {
		var err error
		ldapValues, err = getLDAPValues(username)
		if err != nil {
			return 0, errors.Wrap(err, "unable to get ldap fields from server")
		}
	}

	values = append(values, username)
	for _, field := range frm.Fields {
		var val interface{}
		var err error

		// ldap fields first, if it's an insert
		if field.IsLDAPPopulated && isInsert {
			val = ldapValues[field.Name]
			values = append(values, val)
			continue
		}

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

func ServeForm(w http.ResponseWriter, req *http.Request) {
	var err error

	vars := mux.Vars(req)
	formPath, exists := vars["table_name"]
	if !exists {
		http.Error(w, "Check form path", http.StatusNotFound)
		return
	}

	entryIdStr, exists := vars["id"]
	entryId, _ := strconv.Atoi(entryIdStr)

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

	creds := goidentity.FromHTTPRequestContext(req)
	username := "anonymous"
	if creds != nil {
		username = creds.UserName()
	} else if !frm.AllowAnonymous {
		http.Error(
			w,
			"Check active directory integration - unable to determine logged in user",
			http.StatusUnauthorized,
		)
		return
	}

	if req.Method == http.MethodGet {

		// TODO: this is in every request for development purposes, can remove when done.
		formTemplate, err = template.ParseFiles("index.template.html")
		if err != nil {
			log.Println(err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		vals := map[string]string{}
		if entryId > 0 {
			vals, err = loadFormEntry(ctx, username, entryId, frm)
			if err != nil {
				log.Println(err)
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			vals["id"] = entryIdStr
		}

		err = formTemplate.Execute(w, map[string]interface{}{"frm": frm, "vals": vals, "username": username})
		if err != nil {
			log.Println(err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	} else if req.Method == http.MethodPost {

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
	} else if !frm.AllowAnonymous {
		http.Error(
			w,
			"Check active directory integration - unable to determine logged in user",
			http.StatusUnauthorized,
		)
		return
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
	err = listTemplate.Execute(w, map[string]interface{}{"frm": frm, "vals": vals, "username": username})
	if err != nil {
		log.Println(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

func parseTemplates(conf tomlConfig) {
	var err error
	formTemplate, err = template.ParseFiles(conf.Server.Template)
	if err != nil {
		log.Fatal(err)
	}
	listTemplate, err = template.ParseFiles("list.template.html")
	if err != nil {
		log.Fatal(err)
	}
}

func serve(conf tomlConfig) {
	var err error
	parseTemplates(conf)
	// start listening
	r := mux.NewRouter()
	r.PathPrefix("/static/").Handler(
		http.StripPrefix("/static/", http.FileServer(http.Dir(conf.Server.StaticDir))),
	)
	r.HandleFunc("/{table_name}/edit/{id:[0-9]+}", ServeForm)
	r.HandleFunc("/{table_name}/list", ServeFormListEntries)
	r.HandleFunc("/{table_name}", ServeForm)
	r.HandleFunc("/", ServeForm)

	if conf.Auth.Keytab != "" {
		sm, err := spnegoFromKeytab(conf.Auth.Keytab, conf.Auth.SessionKey, conf.Auth.CookieName)
		if err != nil {
			log.Fatal(err)
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
