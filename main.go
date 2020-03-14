package main

import "log"

func main() {
	conf := parseConfig("config.toml")
	connectToDb(conf)
	// close db gracefully on shutdown
	defer func() {
		err := db.Close()
		if err != nil {
			log.Fatal(err)
		}
	}()
	serve(conf)
}
