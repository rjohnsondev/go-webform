package main

import (
	"fmt"
	"github.com/shopspring/decimal"
	"html/template"
	"log"
	"time"
)

// the fact that golang numbers elements based on US layout is frustrating
// 01 == month, 02 == day
const DateTimeLocal = "2006-01-02T15:04"
const DateLocal = "2006-01-02"

type Form struct {
	Name                     string
	Description              string
	TableName                string
	Fields                   []*FormField
	PreviouslyInsertedRecord string
	Admins                   map[string]bool
	AllowAnonymous           bool
	UseLDAPFields            bool
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
	IsLDAPPopulated  bool
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
		case "decimal":
			return FormDecimal
		case "money":
			return FormMoney
		case "float":
			return FormFloat
		case "bit":
			return FormBoolean
		case "datetimeoffset":
			return FormTimeStamp
		case "date":
			return FormDate
		}
	}
	return FormVarChar
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
		return new(decimal.Decimal)
	case FormMoney:
		return new(decimal.Decimal)
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
		return string(val.([]uint8))
	case FormMoney:
		d, err := decimal.NewFromString(string(val.([]uint8)))
		if err != nil {
			log.Printf("Error reading decimal: %v", err)
			return string(val.([]uint8))
		}
		return d.StringFixed(2)
	case FormFloat:
		return fmt.Sprintf("%v", val.(float64))
	case FormSelect:
		return val.(string)
	case FormRadio:
		return val.(string)
	case FormTimeStamp:
		return val.(time.Time).Format(DateTimeLocal)
	case FormDate:
		return val.(time.Time).Format(DateLocal)
	case FormBoolean:
		if val.(bool) {
			return "1"
		} else {
			return ""
		}
	}
	return ""
}
