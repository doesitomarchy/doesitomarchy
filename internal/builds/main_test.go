package builds

import (
	"os"
	"testing"

	"github.com/doesitomarchy/doesitomarchy/internal/testdb"
)

func TestMain(m *testing.M) {
	code := m.Run()
	testdb.Cleanup()
	os.Exit(code)
}
