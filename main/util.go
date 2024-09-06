package main

import "fmt"

const (
	ANSI_ERASE_LINE = "\x1b[2K\r"
	ANSI_RESET      = "\x1b[0m"

	ANSI_BLACK  = "\x1b[30m"
	ANSI_RED    = "\x1b[31m"
	ANSI_GREEN  = "\x1b[32m"
	ANSI_YELLOW = "\x1b[33m"
	ANSI_BLUE   = "\x1b[34m"
	ANSI_PURPLE = "\x1b[35m"
	ANSI_CYAN   = "\x1b[36m"
	ANSI_WHITE  = "\x1b[37m"
)

type FlagStringSlice []string

func (f *FlagStringSlice) String() string {
	return fmt.Sprintf("%v", *f)
}

func (f *FlagStringSlice) Set(value string) error {
	*f = append(*f, value)
	return nil
}
