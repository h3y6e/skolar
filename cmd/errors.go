package cmd

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

const (
	codeFailed               = "failed"
	codeUsage                = "usage"
	codeConfirmationRequired = "confirmation_required"
	codeDeclined             = "declined"
	codeSkillNotFound        = "skill_not_found"
	codeCheckFailed          = "check_failed"
	codeVerifyFailed         = "verify_failed"
)

type cliError struct {
	code string
	err  error
	fix  string
}

func (e *cliError) Error() string { return e.err.Error() }
func (e *cliError) Unwrap() error { return e.err }

func usageError(c *cobra.Command, err error) error {
	return &cliError{codeUsage, err, c.CommandPath() + " --help"}
}

func usageArgs(validate cobra.PositionalArgs) cobra.PositionalArgs {
	return func(c *cobra.Command, args []string) error {
		if err := validate(c, args); err != nil {
			return usageError(c, err)
		}

		return nil
	}
}

func checkFailed(names []string) error {
	if len(names) == 0 {
		return nil
	}

	return &cliError{code: codeCheckFailed, err: fmt.Errorf("%s を確認できませんでした", strings.Join(names, ", "))}
}

type errorRecord struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Fix     string `json:"fix,omitempty"`
}

func newErrorRecord(err error) errorRecord {
	rec := errorRecord{Code: codeFailed, Message: err.Error()}
	if ce, ok := errors.AsType[*cliError](err); ok {
		rec.Code, rec.Fix = ce.code, ce.fix
	}

	return rec
}
