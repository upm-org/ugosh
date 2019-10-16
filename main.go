// Copyright (c) 2017, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/crypto/ssh/terminal"

	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

type arrayFlags []string

func (i *arrayFlags) String() string {
	return fmt.Sprintf("%v", *i)
}

func (i *arrayFlags) Set(value string) error {
	*i = strings.Split(value, ",")
	return nil
}

type concErrors []error

func (e *concErrors) HasError() bool {
	hasError := false
	for _, err := range *e {
		if err != nil {
			hasError = true
			break
		}
	}
	return hasError
}

func (e *concErrors) Add(err error) {
	*e = append(*e, err)
}

func (e *concErrors) GetError() error {
	var b strings.Builder
	hasError := false

	for _, err := range *e {
		if err != nil {
			b.Grow(len(err.Error()) + 1)
			b.WriteString(err.Error())
			b.WriteRune('\n')
			hasError = true
		}
	}

	if hasError {
		return errors.New(b.String())
	}

	return nil
}

var command = flag.String("c", "", "command to be executed")

var concArgs arrayFlags

func main() {
	flag.Var(&concArgs, "a", "files to be executed concurrently (separated by comma)")
	flag.Parse()
	err := runAll()
	if e, ok := err.(interp.ExitStatus); ok {
		os.Exit(int(e))
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func runAll() error {
	errChan := make(chan error, len(concArgs))

	seqRunner, err := interp.New(interp.StdIO(os.Stdin, os.Stdout, os.Stderr))
	if err != nil {
		return err
	}

	if *command != "" {
		return run(seqRunner, strings.NewReader(*command), "")
	}
	if flag.NArg() == 0 && flag.NFlag() == 0 {
		if terminal.IsTerminal(int(os.Stdin.Fd())) {
			return runInteractive(seqRunner, os.Stdin, os.Stdout, os.Stderr)
		}
		return run(seqRunner, os.Stdin, "")
	}

	for _, path := range flag.Args() {
		if err := runPath(seqRunner, path); err != nil {
			return err
		}
	}

	for _, arg := range concArgs {
		go func(p string){
			concRunner, err := interp.New(interp.StdIO(os.Stdin, os.Stdout, os.Stderr))
			if err != nil {
				errChan <- err
				return
			}

			errChan <- runPath(concRunner, p)
		}(arg)
	}

	errs := &concErrors{}
	for i := 0; i < len(concArgs); i++ {
		errs.Add(<-errChan)
	}
	if err = errs.GetError(); err != nil {
		return err
	}

	return nil
}

func run(r *interp.Runner, reader io.Reader, name string) error {
	prog, err := syntax.NewParser().Parse(reader, name)
	if err != nil {
		return err
	}
	r.Reset()
	ctx := context.Background()
	return r.Run(ctx, prog)
}

func runPath(r *interp.Runner, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return run(r, f, path)
}

func runInteractive(r *interp.Runner, stdin io.Reader, stdout, stderr io.Writer) error {
	parser := syntax.NewParser()
	fmt.Fprintf(stdout, "$ ")
	var runErr error
	fn := func(stmts []*syntax.Stmt) bool {
		if parser.Incomplete() {
			fmt.Fprintf(stdout, "> ")
			return true
		}
		ctx := context.Background()
		for _, stmt := range stmts {
			runErr = r.Run(ctx, stmt)
			if r.Exited() {
				return false
			}
		}
		fmt.Fprintf(stdout, "$ ")
		return true
	}
	if err := parser.Interactive(stdin, fn); err != nil {
		return err
	}
	return runErr
}
