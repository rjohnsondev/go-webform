package main

import (
	"github.com/BurntSushi/toml"
	"io/ioutil"
	"log"
)

type tomlConfig struct {
	Server   serverConfig
	LDAP     ldapConfig
	Database databaseConfig
	Auth     authConfig
}

type ldapConfig struct {
	Host     string
	Username string
	Password string
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

func parseConfig(confFile string) tomlConfig {

	tomlData, err := ioutil.ReadFile(confFile)
	if err != nil {
		log.Fatalf("unable to load config file: config.toml: %s\n", err)
	}

	var conf tomlConfig
	if _, err := toml.Decode(string(tomlData), &conf); err != nil {
		log.Fatalf("unable to load config file: %s\n", err)
	}

	return conf
}
