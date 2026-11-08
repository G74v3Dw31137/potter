package botguard

import (
	"context"
	_ "embed"
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"github.com/dop251/goja"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func imageM1Probe(t *testing.T) []byte {
	t.Helper()
	m, _ := newFake(t)
	ps, e := m.staticPrograms(context.Background())
	require.NoError(t, e)
	rt := newRuntime(fixtureContext())
	e = rt.initialize(context.Background(), fixtureContext(), ps)
	require.NoError(t, e)
	b, e := os.ReadFile("testdata/image-m1-probe.js")
	require.NoError(t, e)
	v, e := rt.vm.RunString(string(b))
	require.NoError(t, e)
	return []byte(v.String())
}

func TestImageM1Baseline(t *testing.T) {
	current := imageM1Probe(t)
	b, e := os.ReadFile("testdata/image-m1-expectations.json")
	require.NoError(t, e)
	var cases map[string]differentialCase
	e = json.Unmarshal(b, &cases)
	require.NoError(t, e)
	check := func(t *testing.T, b []byte, reference bool) {
		var actual map[string]any
		require.NoError(t, json.Unmarshal(b, &actual))
		require.Len(t, actual, len(cases), "observation count changed")
		for k, c := range cases {
			same := reflect.DeepEqual(c.Current, c.Reference)
			if same {
				require.Equal(t, "agreement", c.Classification, "invalid classification: %s", k)
			} else {
				require.Contains(t, []string{"known-defect", "reference-disagreement"}, c.Classification, "invalid classification: %s", k)
			}
			t.Run(k, func(t *testing.T) {
				want := c.Current
				if reference {
					want = c.Reference
				}
				actualValue, exists := actual[k]
				require.True(t, exists, "image behavior changed\n got %#v\nwant %#v", actual[k], want)
				assert.Equal(t, want, actualValue, "image behavior changed")
			})
		}
	}
	t.Run("goja", func(t *testing.T) { check(t, current, false) })

}

func BenchmarkImageM1(b *testing.B) {
	m := NewEngine(nil)
	defer m.Close()
	ps, e := m.staticPrograms(context.Background())
	if e != nil {
		b.Fatal(e)
	}
	rt := newRuntime(fixtureContext())
	if e = rt.initialize(context.Background(), fixtureContext(), ps); e != nil {
		b.Fatal(e)
	}
	for _, item := range []struct{ name, body string }{
		{"adapter-read", `return image.border+image.hspace+image.complete+image.currentSrc;`},
		{"vendor-read", `return image.src+image.width+image.alt;`},
		{"adapter-write", `image.border='2';image.hspace=3;image.align='left';`},
	} {
		b.Run(item.name, func(b *testing.B) {
			v, e := rt.vm.RunString(`(function(){const image=document.createElement('img');image.border='1';image.hspace=2;return function(){` + item.body + `};})()`)
			if e != nil {
				b.Fatal(e)
			}
			fn, _ := goja.AssertFunction(v)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, e = fn(goja.Undefined()); e != nil {
					b.Fatal(e)
				}
			}
		})
	}
}

func TestWebIDLToolkit(t *testing.T) {
	vm := goja.New()
	{
		_, e := vm.RunString(webIDLJS)
		require.NoError(t, e)
	}
	script, e := os.ReadFile("testdata/webidl-tests.js")
	require.NoError(t, e)
	v, e := vm.RunString(string(script))
	require.NoError(t, e)
	var cases map[string]any
	e = json.Unmarshal([]byte(v.String()), &cases)
	require.NoError(t, e)
	require.GreaterOrEqual(t, len(cases), 40, "toolkit coverage missing")
	for name, result := range cases {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, true, result)
		})
	}
}

//go:embed assets/legacy-image-state-v1.js
var legacyImageStateV1JS string

// The archived image bytes are a reproducible public regression oracle.
// This test never changes production installation order.
func TestImageM1LegacyEquivalence(t *testing.T) {
	var observations []string
	for _, historical := range []bool{true, false} {
		var ps []*goja.Program
		for _, source := range staticSources() {
			if historical && source.Name == "webidl" {
				continue
			}
			if historical && source.Name == "image-state" {
				source.Source = legacyImageStateV1JS
			}
			p, e := goja.Compile("botguard-bundle.js", source.Source, false)
			require.NoError(t, e)
			ps = append(ps, p)
		}
		rt := newRuntime(fixtureContext())
		require.NoError(t, rt.initialize(context.Background(), fixtureContext(), ps))
		probe, e := os.ReadFile("testdata/image-m1-probe.js")
		require.NoError(t, e)
		v, e := rt.vm.RunString(string(probe))
		require.NoError(t, e)
		observations = append(observations, v.String())
		v, e = rt.vm.RunString(`(function(){
   const out=[],image=document.createElement('img'),proto=HTMLImageElement.prototype;
   // User exception identity and failed conversion preserve the content attribute.
   for(const name of ['border','hspace','vspace','longDesc','align','name','useMap']) {
    image.setAttribute(name.toLowerCase(),'old');const error={};let caught;
    try {image[name]={[Symbol.toPrimitive](){throw error;}};}catch(e){caught=e;}
    out.push([name,caught===error,image.getAttribute(name.toLowerCase())]);
   }
   out.push(['privateExports',Object.hasOwn(globalThis,'pocWebIDL'),Object.hasOwn(globalThis,'pocResolveDOMURL')]);
   // Preserve call-time global lookups used by legacy accessors.
   const originalString=String, originalTypeError=TypeError, originalImage=LD.HTMLImageElement;
   try {
    globalThis.String=function(){return 'custom';};image.border=3;out.push(image.getAttribute('border'));
    globalThis.TypeError=class PublicError extends Error{};
    try{Object.getOwnPropertyDescriptor(proto,'complete').get.call({});}catch(e){out.push([e.constructor.name,e.message]);}
    LD.HTMLImageElement=class PublicImage{};
    try{out.push(image.complete);}catch(e){out.push([e.constructor.name,e.message]);}
   }finally{globalThis.String=originalString;globalThis.TypeError=originalTypeError;LD.HTMLImageElement=originalImage;}
   // Parser/clone/namespace factories keep their vendor prototypes and reflection.
   const container=document.createElement('div');container.innerHTML='<img src="a.png" width="-1" border="2">';
   const parsed=container.firstChild,clone=parsed.cloneNode(true),ns=document.createElementNS('http://www.w3.org/1999/xhtml','img');
   for(const item of [parsed,clone,ns]) out.push([item instanceof HTMLImageElement,item.src,item.width,item.border,item.currentSrc]);
   return JSON.stringify(out);
  })()`)
		require.NoError(t, e)
		observations = append(observations, v.String())
	}
	assert.Equal(t, observations[0], observations[2], "historical and migrated public image observations differ")
	assert.Equal(t, observations[1], observations[3], "historical and migrated public image observations differ")
}

func BenchmarkWebIDLInstall(b *testing.B) {
	program, e := goja.Compile("webidl.js", webIDLJS, false)
	if e != nil {
		b.Fatal(e)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vm := goja.New()
		if _, e = vm.RunProgram(program); e != nil {
			b.Fatal(e)
		}
	}
}
