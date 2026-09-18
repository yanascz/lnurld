package main

import (
	"github.com/stretchr/testify/assert"
	"os"
	"testing"
)

func TestWriteObject(t *testing.T) {
	fileName := t.TempDir() + pathSeparator + "state" + jsonExtension
	state := UserState{AccountInvoicesCounts: map[AccountKey]int{"21": 42}}

	assert.NoError(t, writeObject(fileName, &state))

	var written UserState
	assert.NoError(t, readObject(fileName, &written))
	assert.Equal(t, state, written)

	// an overwrite has to leave the file readable, and no leftovers behind
	state.AccountInvoicesCounts["21"] = 43
	assert.NoError(t, writeObject(fileName, &state))
	assert.NoError(t, readObject(fileName, &written))
	assert.Equal(t, state, written)

	_, err := os.Stat(fileName + tempExtension)
	assert.True(t, os.IsNotExist(err), "temporary file left behind")
}
