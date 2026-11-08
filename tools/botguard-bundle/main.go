// Run from this directory: GOWORK=off go run . [-check | -licenses | -licenses-check]
// Bundle modes use local pinned sources; license modes fetch pinned archives.
// Normal library builds do not run this tool.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/evanw/esbuild/pkg/api"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("botguard-bundle", flag.ContinueOnError)
	flags.SetOutput(stderr)
	licenses := flags.Bool("licenses", false, "refresh THIRD_PARTY.md from integrity-checked pinned npm archives")
	licensesCheck := flags.Bool("licenses-check", false, "verify generated THIRD_PARTY.md notices without writing files")
	check := flags.Bool("check", false, "verify both bundles byte-for-byte without writing assets or using the network")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 || (*check && (*licenses || *licensesCheck)) || (*licenses && *licensesCheck) {
		fmt.Fprintln(stderr, "use -check, -licenses or -licenses-check separately, with no positional arguments")
		return 2
	}
	root, err := filepath.Abs("../../internal/botguard")
	if err == nil {
		err = execute(root, *check, *licenses, *licensesCheck)
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if *check {
		fmt.Fprintln(stdout, "verified assets/bgutils.js and assets/linkedom.js (including legal comments)")
	}
	if *licenses || *licensesCheck {
		fmt.Fprintln(stdout, "verified generated npm licenses in THIRD_PARTY.md")
	}
	return 0
}

func execute(root string, check, licenses, licensesCheck bool) error {
	raw, err := os.ReadFile(filepath.Join(root, "sources.json"))
	if err != nil {
		return err
	}
	var sources map[string]string
	if err = json.Unmarshal(raw, &sources); err != nil {
		return err
	}
	for name, expected := range sources {
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			return err
		}
		if fmt.Sprintf("%x", sha256.Sum256(data)) != expected {
			return fmt.Errorf("source integrity mismatch: %s", name)
		}
	}
	if licenses || licensesCheck {
		return updateLicenses(root, licensesCheck)
	}
	return bundles(root, check)
}

func bundles(root string, check bool) error {
	options := api.BuildOptions{Bundle: true, Format: api.FormatIIFE, Target: api.ES2017, Write: !check, LegalComments: api.LegalCommentsInline, LogLevel: api.LogLevelWarning, GlobalName: "BG", Outfile: filepath.Join(root, "assets/bgutils.js"), Stdin: &api.StdinOptions{Contents: `export {BotGuardClient} from './core/BotGuardClient.ts';export {WebPoMinter} from './core/WebPoMinter.ts';`, ResolveDir: filepath.Join(root, "upstream/bgutils"), Sourcefile: "bgutils-entry.ts", Loader: api.LoaderTS}}
	var mismatches []string
	for _, artifact := range []string{"bgutils.js", "linkedom.js"} {
		if artifact == "linkedom.js" {
			options.Stdin = nil
			options.EntryPoints = []string{filepath.Join(root, "upstream/linkedom-worker.js")}
			options.GlobalName = "LD"
			options.Outfile = filepath.Join(root, "assets/linkedom.js")
		}
		result := api.Build(options)
		if len(result.Errors) > 0 {
			return fmt.Errorf("bundle failed: assets/%s", artifact)
		}
		if !check {
			continue
		}
		if len(result.OutputFiles) != 1 {
			return fmt.Errorf("unexpected output for assets/%s", artifact)
		}
		committed, err := os.ReadFile(options.Outfile)
		if err != nil {
			return fmt.Errorf("read assets/%s: %w", artifact, err)
		}
		if !bytes.Equal(committed, result.OutputFiles[0].Contents) {
			mismatches = append(mismatches, "assets/"+artifact)
		}
	}
	if len(mismatches) > 0 {
		return fmt.Errorf("bundle mismatch: %v", mismatches)
	}
	return nil
}
