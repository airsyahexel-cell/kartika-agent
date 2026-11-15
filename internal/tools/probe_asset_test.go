package tools

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestAssetEnumeration(t *testing.T) {
	if testing.Short() {
		t.Skip("butuh jaringan")
	}
	reg := NewRegistry()
	RegisterBuiltins(reg, ".")
	out := reg.Call("http_probe", json.RawMessage(`{"url":"http://testaspnet.vulnweb.com/login.aspx","max_body_bytes":16384}`))
	assets := false
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "Aset & Tautan") {
			assets = true
		}
		if assets {
			fmt.Println(l)
		}
	}
}
