// Package publisher is the publisher's side of sourced.net: it turns a web
// root into a signed /.well-known/sourced/ tree, tracks changes between
// builds, and manages the publisher's keys.
//
// A project is a sourced.json config next to a web root and a private key
// directory. Private keys never live inside the web root.
package publisher
