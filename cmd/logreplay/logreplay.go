package main

import (
	"os"
	"github.com/aryszka/logreplay"
	"golang.org/x/crypto/ssh/terminal"
	"io"
	"flag"
	"errors"
	"log"
	"fmt"
)

var (
	errTooManyInput = errors.New("too many input")
	errNoInput = errors.New("no input defined")
)

func input() (io.Reader, error) {
	args := flag.Args()
	if len(args) > 1 {
		return nil, errTooManyInput
	}

	if len(args) == 1 {
		return os.Open(args[0])
	}

	fdint := int(os.Stdin.Fd())
	if terminal.IsTerminal(fdint) {
		return os.Stdin, nil
	}

	return nil, errNoInput
}

func play(p *logreplay.Player) {
	playFunc := p.Play
	if once {
		playFunc = p.Once
	}

	err := playFunc()
	if err != nil {
		log.Fatal(err)
	}
}

func playControl(p *logreplay.Player) {
	log.Println("press Enter to pause or play")
	var running bool
	for {
		if running {
			p.Pause()
			log.Println("paused")
			running = false
		} else {
			go play(p)
			log.Println("playing")
			running = true
		}

		fmt.Scanln()
	}
}

func main() {
	input, err := input()
	if err != nil {
		log.Fatal(err)
	}

	options.AccessLog = input

	p, err := logreplay.New(options)
	if err != nil {
		log.Fatal(err)
	}

	if input == os.Stdin {
		play(p)
		return
	}

	playControl(p)
	select {}
}
