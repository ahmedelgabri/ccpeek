package server

import (
	"fmt"
	"log"
	"net/http"
)

func serverError(w http.ResponseWriter, action string, err error) {
	if err != nil {
		log.Printf("%s: %v", action, err)
	} else {
		log.Printf("%s", action)
	}
	http.Error(w, fmt.Sprintf("Failed to %s", action), http.StatusInternalServerError)
}
