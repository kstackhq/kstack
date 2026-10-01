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

package credentials

import (
	"errors"
	"strings"
)

var errBatchArg = errors.New("credentials: an argument holds a character a batch file cannot take")

// batchLine is bin and args as cmd.exe runs a batch file with them: each in double
// quotes, where cmd.exe reads &, |, <, >, ( and ) as text. Nothing escapes a % or
// a " inside them, so a value holding either is refused, and a trailing backslash
// is doubled so the program the batch file hands them to does not read it as
// escaping the closing quote.
func batchLine(bin string, args []string) (string, error) {
	quoted := make([]string, 0, len(args)+1)
	for _, v := range append([]string{bin}, args...) {
		if strings.ContainsAny(v, "\"%\r\n\x00") {
			return "", errBatchArg
		}
		trimmed := strings.TrimRight(v, `\`)
		quoted = append(quoted, `"`+trimmed+strings.Repeat(`\`, 2*(len(v)-len(trimmed)))+`"`)
	}
	return strings.Join(quoted, " "), nil
}
