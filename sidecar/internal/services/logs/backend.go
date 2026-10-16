// Copyright 2026 The Kstack Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package logs

import (
	"context"
	"errors"
	"net/http"
)

// Backend reads streams' lines, so a read has one shape whoever serves it. A read
// is a stream: the caller pulls its lines one at a time and cancels it once it
// has what it needs, and once its lines close it says how it ended. Each read
// keeps within o's bounds, budget and grep.
type Backend interface {
	// Forward reads oldest first.
	Forward(ctx context.Context, st stream, o readOpts) *streamRead
	// Backward reads newest first.
	Backward(ctx context.Context, st stream, o readOpts) *streamRead
	// Tail is Forward that keeps reading as the log grows.
	Tail(ctx context.Context, st stream, o readOpts) *streamRead
}

// failure is how an error ended one stream's read.
type failure int

const (
	// failNone is a read that ended clean.
	failNone failure = iota
	// failFatal is the cluster refusing the user's credentials, or Kstack no
	// longer connecting to it: it fails the whole page or follow.
	failFatal
	// failDenied is a 403: access to pods/log is granted per namespace, so it is
	// this stream's alone.
	failDenied
	// failEnded is any other status under 500, the server's answer for this log
	// now (a container waiting to start, a pod gone).
	failEnded
	// failTransient is a 5xx, a 408, a 429 or a transport error, which a follow
	// retries: a request the server timed out or throttled says nothing about
	// the log.
	failTransient
)

// failureKind is how err ended a stream's read.
func failureKind(err error) failure {
	var se *statusError
	switch {
	case err == nil:
		return failNone
	case errors.Is(err, ErrRevoked):
		return failFatal
	case !errors.As(err, &se) || se.code >= http.StatusInternalServerError,
		se.code == http.StatusRequestTimeout, se.code == http.StatusTooManyRequests:
		return failTransient
	case se.code == http.StatusUnauthorized:
		return failFatal
	case se.code == http.StatusForbidden:
		return failDenied
	}
	return failEnded
}

// fatalError is what err fails a whole page or follow with, nil unless it is
// fatal.
func fatalError(err error) error {
	if failureKind(err) != failFatal {
		return nil
	}
	var se *statusError
	if errors.As(err, &se) {
		return Refusal{"the cluster refused to show this log: " + se.Error()}
	}
	return ErrRevoked
}
