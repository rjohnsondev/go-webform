package main

import "log"

func main() {
	conf := parseConfig("config.toml")

	if conf.LDAP.Host != "" {
		connectToLDAP(conf)
	}

	connectToDb(conf)

	defer func() {
		err := db.Close()
		if err != nil {
			log.Fatal(err)
		}
	}()
	serve(conf)
}
