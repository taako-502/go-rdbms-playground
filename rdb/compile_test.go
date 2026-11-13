package rdb_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestTypedAPICompileContract(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate module")
	}
	root := filepath.Dir(filepath.Dir(file))
	dir := t.TempDir()
	mod := fmt.Sprintf("module compilecheck\n\ngo 1.27.0\n\nrequire github.com/taako-502/go-rdbms-playground v0.0.0\nreplace github.com/taako-502/go-rdbms-playground => %q\n", root)
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(mod), 0600); err != nil {
		t.Fatal(err)
	}
	prefix := `package main
import("context";rdb "github.com/taako-502/go-rdbms-playground/rdb")
type User struct { ID int64 }
type Other struct { ID int64 }
func main(){
var db *rdb.DB
users,_:=db.Table[User]("users")
id,_:=users.Column[int64]("ID")
_=users;_=id;_=context.Background()
`
	for _, tt := range []struct {
		name, code string
		valid      bool
	}{
		{"valid", `_ = users.Insert(context.Background(),User{}); _,_ = users.Where(rdb.Equal(id,20)).All(context.Background())`, true},
		{"wrong_comparison_type", `_ = rdb.Equal(id,"20")`, false},
		{"wrong_insert_model", `_ = users.Insert(context.Background(),Other{})`, false},
		{"wrong_predicate_model", `others,_:=db.Table[Other]("others"); otherID,_:=others.Column[int64]("ID"); _ = users.Where(rdb.Equal(otherID,20))`, false},
		{"wrong_result_type", `var rows []Other; rows,_=users.All(context.Background());_=rows`, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(prefix+tt.code+"\n}"), 0600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("go", "build", "-o", filepath.Join(dir, "program"), ".")
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "GOWORK=off", "GOPROXY=off", "GOSUMDB=off", "GOTOOLCHAIN=local")
			output, err := cmd.CombinedOutput()
			if (err == nil) != tt.valid {
				t.Fatalf("valid=%v error=%v\n%s", tt.valid, err, output)
			}
		})
	}
}
