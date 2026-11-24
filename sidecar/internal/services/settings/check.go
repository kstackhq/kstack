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

package settings

// checks is the file's read-back: one check per field. The file has no field
// left, so there is none.
var checks []func(*Settings) []Refusal

// strictest is how each field that restricts is set to its most restrictive
// state while the file holds a value the store refuses. The file has no field
// left, so there is none.
var strictest = map[string]func(*Settings){}
