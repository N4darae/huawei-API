package fw

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
)

var (
	ErrRulesetFlush   = errors.New("fw: the rendered ruleset contains a ruleset-wide flush")
	ErrTableMissing   = errors.New("fw: table is missing")
	ErrChainMissing   = errors.New("fw: chain is missing")
	ErrSetMissing     = errors.New("fw: set is missing")
	ErrElementMissing = errors.New("fw: element is not a member of the set after adding it")
	ErrRuleOrder      = errors.New("fw: proxy_egress rules are not in the measured order")
	ErrNoCustomerRule = errors.New("fw: the customer-facing accept rule is missing")
	ErrBadIface       = errors.New("fw: interface name is empty")
	ErrBadAddr        = errors.New("fw: address is not a valid ipv4 address")
)

const forbiddenFlush = "flush" + " " + "ruleset"

type Exec func(ctx context.Context, stdin []byte, name string, args ...string) ([]byte, error)

func SystemExec(ctx context.Context, stdin []byte, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if len(stdin) > 0 {
		cmd.Stdin = strings.NewReader(string(stdin))
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, &CommandError{Name: name, Args: args, Output: string(out), Err: err}
	}
	return out, nil
}

type CommandError struct {
	Name   string
	Args   []string
	Output string
	Err    error
}

func (e *CommandError) Error() string {
	return fmt.Sprintf("fw: %s %s: %v: %s", e.Name, strings.Join(e.Args, " "), e.Err, strings.TrimSpace(e.Output))
}

func (e *CommandError) Unwrap() error { return e.Err }

var absentSignatures = []string{
	"no such file or directory",
	"does not exist",
	"not found",
	"no such element",
	"cannot find device",
	"no such device",
	"could not process rule: no such file or directory",
}

func IsAbsent(err error) bool {
	if err == nil {
		return false
	}
	for _, e := range []error{syscall.ENOENT, syscall.ENODEV, syscall.ESRCH} {
		if errors.Is(err, e) {
			return true
		}
	}
	text := err.Error()
	var ce *CommandError
	if errors.As(err, &ce) {
		text = ce.Output + " " + text
	}
	text = strings.ToLower(text)
	for _, sig := range absentSignatures {
		if strings.Contains(text, sig) {
			return true
		}
	}
	return false
}

func IgnoreAbsent(err error) error {
	if IsAbsent(err) {
		return nil
	}
	return err
}

const CommentLoopbackLeg = "farm-local probe leg"

func EgressRuleOrder() []string {
	return []string{
		CommentCustomerLeg,
		CommentLoopbackLeg,
		"fence tcp reset",
		"fence icmpx",
		"dns to dongle gateway",
		"ssrf log",
		"ssrf drop",
		"leak log",
		"leak drop",
		"smtp drop",
		"new connection rate limit",
		"default accept",
	}
}
