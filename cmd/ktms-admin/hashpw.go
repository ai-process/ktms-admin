package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/ai-process/ktms-admin/internal/auth/password"
)

const hashUsage = `usage: ktms-admin hash-password [email]

Reads a password (twice, without echo, on a terminal; one line otherwise) and
prints its argon2id hash, or an ADMIN_USERS entry when an e-mail is given.`

// hashPassword prints a hash for ADMIN_USERS and returns the exit code.
func hashPassword(args []string) int {
	if len(args) > 1 || (len(args) == 1 && !strings.Contains(args[0], "@")) {
		fmt.Fprintln(os.Stderr, hashUsage)
		return 2
	}
	pw, err := readPassword()
	if err != nil {
		fmt.Fprintln(os.Stderr, "hash-password:", err)
		return 1
	}
	if len([]rune(pw)) < password.MinLength {
		fmt.Fprintf(os.Stderr, "hash-password: use at least %d characters\n", password.MinLength)
		return 1
	}
	h := password.Hash(pw)
	if len(args) == 1 {
		h = strings.ToLower(args[0]) + ":" + h
	}
	fmt.Println(h)
	return 0
}

func readPassword() (string, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil && line == "" {
			return "", fmt.Errorf("no password on stdin")
		}
		return strings.TrimRight(line, "\r\n"), nil
	}
	fmt.Fprint(os.Stderr, "Password: ")
	a, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	fmt.Fprint(os.Stderr, "Again: ")
	b, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	if string(a) != string(b) {
		return "", fmt.Errorf("the passwords do not match")
	}
	return string(a), nil
}
