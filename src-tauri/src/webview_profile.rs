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

//! Where the webview keeps its profile, which holds `localStorage`, on Linux
//! and Windows. macOS's WKWebView takes no path and keeps its own under the
//! bundle id.

use std::path::PathBuf;

use tauri::{AppHandle, Manager, Runtime};

use crate::error::Result;
use crate::paths::{self, Roots};

/// Where the profile is ([`manage`]).
struct Location(PathBuf);

/// Manages the profile's [`Location`]. `setup` calls it before the first
/// window, whose webview opens the profile. An error fails the launch: a
/// window without a location would keep its profile under the bundle id,
/// outside the sidecar's fence.
pub fn manage<R: Runtime>(app: &AppHandle<R>, roots: &Roots) -> Result<()> {
    app.manage(Location(paths::webview_dir(roots)?));
    Ok(())
}

/// The profile directory, as [`manage`] resolved it.
pub fn dir<R: Runtime>(app: &AppHandle<R>) -> Option<PathBuf> {
    app.try_state::<Location>()
        .map(|location| location.0.clone())
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::testutil::{roots, Scratch};

    #[test]
    fn manage_sets_the_directory_dir_reads() {
        let scratch = Scratch::new();
        let roots = roots(scratch.path());
        let app = tauri::test::mock_app();
        assert_eq!(dir(app.handle()), None);

        manage(app.handle(), &roots).unwrap();

        let expected = paths::app_state_dir(&roots).unwrap().join("webview");
        assert_eq!(dir(app.handle()), Some(expected));
    }

    #[cfg(target_os = "linux")]
    #[test]
    fn a_state_dir_that_cannot_be_made_is_an_error() {
        let scratch = Scratch::new();
        // A file where the state root should be: nothing can be made under it.
        let state = scratch.path().join("file");
        std::fs::write(&state, "").unwrap();
        let roots = Roots {
            state,
            ..roots(scratch.path())
        };
        let app = tauri::test::mock_app();

        assert!(manage(app.handle(), &roots).is_err());
        assert_eq!(dir(app.handle()), None);
    }
}
