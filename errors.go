package main

import "fmt"

type ClientError struct {
	message string
}

func (e *ClientError) Error() string {
	return e.message
}

type ServerError struct {
	message string
	cause   error
}

func (e *ServerError) Error() string {
	return fmt.Sprintf("%s: %v", e.message, e.cause)
}
