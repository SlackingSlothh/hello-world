package main

import (
	"log"
	"net/http"
)

func main() {
	files := http.FileServer(http.Dir("frontend/dist"))
	log.Fatal(http.ListenAndServe(":8080", files))
}
