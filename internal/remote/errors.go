// SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
// SPDX-License-Identifier: GPL-3.0-only

package remote

import "errors"

// FatalError marks a provider error that applies to the provider as a
// whole, not just one repository. Examples include an invalid token or a
// remote-side repository creation quota.
type FatalError struct {
	Err error
}

func (e *FatalError) Error() string { return e.Err.Error() }
func (e *FatalError) Unwrap() error { return e.Err }

// Fatal wraps err so the orchestrator can stop calling that provider for
// the rest of the run.
func Fatal(err error) error {
	if err == nil {
		return nil
	}
	return &FatalError{Err: err}
}

// IsFatal reports whether err was marked as provider-fatal.
func IsFatal(err error) bool {
	var fatal *FatalError
	return errors.As(err, &fatal)
}
