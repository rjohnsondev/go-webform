package main

import (
	"fmt"
	"github.com/go-ldap/ldap/v3"
	"github.com/pkg/errors"
	"log"
)

var ldapConn *ldap.Conn

func getLDAPValues(accountName string) (map[string]string, error) {

	sreq := &ldap.SearchRequest{
		BaseDN:       "dc=tsa,dc=local",
		Scope:        ldap.ScopeWholeSubtree,
		DerefAliases: ldap.DerefFindingBaseObj,
		SizeLimit:    10,
		TimeLimit:    0,
		TypesOnly:    false,
		Filter:       fmt.Sprintf("(&(objectClass=user)(sAMAccountName=%s))", accountName),
		Attributes: []string{
			"manager", "employeeNumber", "sAMAccountName", "mail", "l",
			"displayName", "department",
		},
		Controls: nil,
	}

	out := make(map[string]string)

	sres, err := ldapConn.SearchWithPaging(sreq, 10)
	if err != nil {
		return nil, errors.Wrap(err, "unable to query ldap for account "+accountName)
	}
	if len(sres.Entries) == 0 {
		return nil, errors.New("unable to find details for user " + accountName)
	}

	e := sres.Entries[0]
	out["user_employee_number"] = e.GetAttributeValue("employeeNumber")
	out["user_display_name"] = e.GetAttributeValue("displayName")
	out["user_department"] = e.GetAttributeValue("department")
	out["user_email"] = e.GetAttributeValue("mail")
	out["user_location"] = e.GetAttributeValue("l")

	m := e.GetAttributeValue("manager")

	msreq := &ldap.SearchRequest{
		BaseDN:       m,
		Scope:        ldap.ScopeWholeSubtree,
		DerefAliases: ldap.DerefFindingBaseObj,
		SizeLimit:    10,
		TimeLimit:    0,
		TypesOnly:    false,
		Filter:       "(objectClass=user)",
		Attributes:   []string{"employeeNumber", "sAMAccountName", "mail", "l", "displayName", "department"},
		Controls:     nil,
	}

	mres, err := ldapConn.SearchWithPaging(msreq, 10)
	if err != nil {
		return nil, errors.Wrap(err, "unable to query ldap for manager account "+m)
	}
	if len(mres.Entries) == 0 {
		return nil, errors.New("unable to find details for manager " + m)
	}

	me := mres.Entries[0]

	out["manager"] = me.GetAttributeValue("sAMAccountName")
	out["manager_employee_number"] = me.GetAttributeValue("employeeNumber")
	out["manager_display_name"] = me.GetAttributeValue("displayName")
	out["manager_department"] = me.GetAttributeValue("department")
	out["manager_email"] = me.GetAttributeValue("mail")
	out["manager_location"] = me.GetAttributeValue("l")

	return out, nil
}

func connectToLDAP(conf tomlConfig) {
	var err error
	ldapConn, err = ldap.Dial("tcp", conf.LDAP.Host)
	if err != nil {
		log.Fatalln("error dialing:", err)
	}
	err = ldapConn.Bind(conf.LDAP.Username, conf.LDAP.Password)
	if err != nil {
		log.Fatalln("error binding:", err)
	}
}
