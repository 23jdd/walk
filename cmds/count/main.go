package main

import "github.com/23jdd/walk"

// / migrate
func main() {
	db, err := walk.Open("walk.db")
	if err != nil {
		panic(err)
	}
	defer db.Close()
}
