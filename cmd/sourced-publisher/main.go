// Command sourced-publisher signs a site's pages for sourced.net: it sets up a project and its keys,
// signs new and changed pages into /.well-known/sourced/, and checks sites
// and signed answers using only public files.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
)

const version = "0.1.0-dev"

const usage = `sourced-publisher — sign a site for sourced.net (spec sourced/1)

Usage:
  sourced-publisher <command> [flags] [arguments]

Commands:
  init      create a project: sourced.json, a first key, and keys.json
  build     sign new and changed pages and update the manifest
  keygen    add a new signing key and retire the current one
  revoke    revoke a key and re-sign everything it signed
  check     validate a project, web root, live domain, or resolver answer
  version   print the version

Run "sourced-publisher <command> -h" for a command's flags.
`

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	cmds := map[string]func(context.Context, []string, io.Writer, io.Writer) int{
		"init":   cmdInit,
		"build":  cmdBuild,
		"keygen": cmdKeygen,
		"revoke": cmdRevoke,
		"check":  cmdCheck,
	}
	switch name := args[0]; name {
	case "version":
		fmt.Fprintln(stdout, version)
		return 0
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	default:
		cmd, ok := cmds[name]
		if !ok {
			fmt.Fprintf(stderr, "sourced-publisher: unknown command %q\n\n%s", name, usage)
			return 2
		}
		return cmd(ctx, args[1:], stdout, stderr)
	}
}
