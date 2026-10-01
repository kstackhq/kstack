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

package errors

import (
	"maps"

	"github.com/vektah/gqlparser/v2/gqlerror"
)

// Custom GraphQL errors, each carrying a stable `code` extension.
var (
	ErrValidationError     = NewError("KSTACK_VALIDATION_ERROR", "Validation error")
	ErrRecordNotFound      = NewError("KSTACK_RECORD_NOT_FOUND", "Record not found")
	ErrUnauthenticated     = NewError("KSTACK_UNAUTHENTICATED", "Authentication required")
	ErrForbidden           = NewError("KSTACK_FORBIDDEN", "Forbidden")
	ErrConflict            = NewError("KSTACK_CONFLICT", "Conflict")
	ErrWatchError          = NewError("KSTACK_WATCH_ERROR", "Watch error")
	ErrServiceUnavailable  = NewError("KSTACK_SERVICE_UNAVAILABLE", "Service unavailable")
	ErrChatContextFull     = NewError("KSTACK_CHAT_CONTEXT_FULL", "Chat is longer than the model can read")
	ErrChatSandboxChanged  = NewError("KSTACK_CHAT_SANDBOX_CHANGED", "Chat's sandbox switch changed")
	ErrMemoryNameTaken     = NewError("KSTACK_MEMORY_NAME_TAKEN", "Memory name taken")
	ErrMemoryFull          = NewError("KSTACK_MEMORY_FULL", "Not enough room for this memory")
	ErrMemorySecret        = NewError("KSTACK_MEMORY_SECRET", "Memory holds a credential")
	ErrInternalServerError = NewError("INTERNAL_SERVER_ERROR", "Internal server error")
)

// Clone is what a resolver returns; never one of the values above. gqlgen stamps
// Path and Locations onto the error it is handed, so a shared value would report the
// first request's field ever after — and two requests failing at once would write to
// it concurrently.
func Clone(e *gqlerror.Error) *gqlerror.Error {
	c := *e
	c.Extensions = maps.Clone(e.Extensions)
	return &c
}

// NewError builds a gqlerror carrying a `code` extension.
func NewError(code string, message string) *gqlerror.Error {
	return &gqlerror.Error{
		Message: message,
		Extensions: map[string]interface{}{
			"code": code,
		},
	}
}

// NewValidationError is a validation error naming the rule that failed.
func NewValidationError(rule string, message string) *gqlerror.Error {
	return &gqlerror.Error{
		Message: message,
		Extensions: map[string]any{
			"code": ErrValidationError.Extensions["code"],
			"rule": rule,
		},
	}
}
