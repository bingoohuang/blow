package util

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMergeCodes(t *testing.T) {
	assert.Equal(t, "", MergeCodes([]string{}))
	assert.Equal(t, "200x2,201,202", MergeCodes([]string{"200", "200", "201", "202"}))
	assert.Equal(t, "200x2,202x2", MergeCodes([]string{"200", "200", "202", "202"}))
	assert.Equal(t, "200,201,202x2", MergeCodes([]string{"200", "201", "202", "202"}))
}
