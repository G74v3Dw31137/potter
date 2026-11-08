package botguard

import (
	"encoding/base64"
	"github.com/dop251/goja"
	"strings"
)

// Byte/UTF-8 adapters needed by BgUtils. Browser DOMException classes are not provided.
func installEncoding(vm *goja.Runtime) {
	vm.Set("atob", func(s string) string {
		s = strings.Map(func(r rune) rune {
			switch r {
			case '\t', '\n', '\f', '\r', ' ':
				return -1
			}
			return r
		}, s)
		if len(s)%4 == 0 {
			if strings.HasSuffix(s, "==") {
				s = s[:len(s)-2]
			} else if strings.HasSuffix(s, "=") {
				s = s[:len(s)-1]
			}
		}
		b, e := base64.RawStdEncoding.DecodeString(s)
		if e != nil {
			panic(vm.NewTypeError("invalid base64: " + e.Error()))
		}
		r := make([]rune, len(b))
		for i, v := range b {
			r[i] = rune(v)
		}
		return string(r)
	})
	vm.Set("btoa", func(s string) string {
		var b []byte
		for _, r := range s {
			if r > 255 {
				panic(vm.NewTypeError("invalid binary string"))
			}
			b = append(b, byte(r))
		}
		return base64.StdEncoding.EncodeToString(b)
	})
	vm.Set("encodeUTF8", func(s string) []byte { return []byte(s) })
}
