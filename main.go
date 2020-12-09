// Command pipescratch manages a scratch file as standard input/output for a
// external command.
//
// pipescratch runs a command and creates a temporary scratch file. Each time
// the file is updated, its contents are passed to the command's standard input.
// Each time the command writes to its standard output or error, the file is
// appended a section at the end with the output as line comments.
package main

import (
	"bufio"
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"

	"github.com/fsnotify/fsnotify"
)

var (
	editorFlag = flag.String("editor", "", "`command` to be invoked with the scratch file location as arg (empty just prints it)")
	extFlag    = flag.String("ext", "sql", "`extension` for scratch file")
	linePrefix = flag.String("line-prefix", "-- ", "prefix for each output line")
)

func main() {
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "Usage of %s:\n", os.Args[0])
		flag.PrintDefaults()
		fmt.Fprintf(flag.CommandLine.Output(), "  [command] [arg1] [arg2] ...\n")
		fmt.Fprintf(flag.CommandLine.Output(), "    	command to manage input/output as a scratch file\n")
	}

	flag.Parse()

	if len(flag.Args()) == 0 {
		fmt.Fprintln(flag.CommandLine.Output(), "Missing non-flag arguments: [command] [arg1] [arg2] ...")
		flag.Usage()
		os.Exit(2)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	f, err := ioutil.TempFile("", "pipescratch-*."+(*extFlag))
	try(err)
	defer f.Close()
	defer os.Remove(f.Name())

	if *editorFlag != "" {
		go exec.CommandContext(ctx, *editorFlag, f.Name()).CombinedOutput()
	} else {
		fmt.Println(f.Name())
	}

	watcher, err := fsnotify.NewWatcher()
	try(err)
	try(watcher.Add(f.Name()))

	cmd := exec.CommandContext(ctx, flag.Args()[0], flag.Args()[1:]...)
	cmdIn, err := cmd.StdinPipe()
	try(err)
	cmdOut, err := cmd.StdoutPipe()
	try(err)
	cmdErr, err := cmd.StderrPipe()
	try(err)
	try(cmd.Start())

	outLines := make(chan string)
	go readLines(outLines, cmdOut)
	errLines := make(chan string)
	go readLines(errLines, cmdErr)

	var currOut, currErr string

	for {
		select {
		case line, ok := <-outLines:
			if !ok {
				outLines = nil
				continue
			}
			currOut += *linePrefix + line + "\n"

		case line, ok := <-errLines:
			if !ok {
				errLines = nil
				continue
			}
			currErr += *linePrefix + line + "\n"

		case ev := <-watcher.Events:
			if ev.Op&fsnotify.Write != fsnotify.Write {
				continue
			}
			_, err := f.Seek(0, 0)
			try(err)

			_, err = io.Copy(cmdIn, f)
			try(err)

			currOut, currErr = "", ""
			continue

		case err := <-watcher.Errors:
			panic(err)
		}

		_, err := f.Seek(0, 0)
		try(err)

		oldContents := bufio.NewReader(f)
		var newContents bytes.Buffer
		for {
			line, err := oldContents.ReadString('\n')
			if line == (*linePrefix)+"~~ scratch ~~\n" {
				break
			}
			newContents.WriteString(line)
			if err != nil {
				newContents.WriteString("\n")
				break
			}
		}
		fmt.Fprintf(&newContents, (*linePrefix)+"~~ scratch ~~\n%s%s", currOut, currErr)

		_, err = f.Seek(0, 0)
		try(err)
		try(f.Truncate(0))
		select {
		case <-watcher.Events:
		case err := <-watcher.Errors:
			panic(err)
		}

		_, err = io.Copy(f, &newContents)
		try(err)
		select {
		case <-watcher.Events:
		case err := <-watcher.Errors:
			panic(err)
		}
	}
}

func readLines(dst chan<- string, src io.Reader) {
	buf := bufio.NewReader(src)
	for {
		line, err := buf.ReadString('\n')
		if err != nil {
			close(dst)
			return
		}
		dst <- line[:len(line)-1]
	}
}

func try(err error) {
	if err != nil {
		panic(err)
	}
}
