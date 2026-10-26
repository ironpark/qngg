package wasmtime

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWasiConfig(t *testing.T) {
	config := NewWasiConfig()
	defer config.Close()
	config.SetEnv([]string{"WASMTIME"}, []string{"GO"})
	err := config.PreopenDir(".", ".", false)
	require.Nil(t, err)
	err = config.PreopenDir(".", ".", true)
	require.Nil(t, err)

}
