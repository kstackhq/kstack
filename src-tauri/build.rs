fn main() {
    // Vendored protoc: keeps `cargo build` hermetic (no system install).
    std::env::set_var(
        "PROTOC",
        protoc_bin_vendored::protoc_bin_path().expect("vendored protoc"),
    );

    // Repo-root proto/ is the shared source of truth (also compiled for the Go
    // sidecar). The host is a client; the server half is for the tests' fake
    // sidecar (`services/sidecar/testutil.rs`).
    tonic_prost_build::configure()
        .compile_protos(
            &["../proto/auth.proto", "../proto/poke.proto"],
            &["../proto"],
        )
        .expect("compile proto");
    println!("cargo:rerun-if-changed=../proto/auth.proto");
    println!("cargo:rerun-if-changed=../proto/poke.proto");

    // tauri-build embeds its Windows manifest in the app binary alone. Without
    // the manifest's Common Controls v6, a test executable fails to load
    // (STATUS_ENTRYPOINT_NOT_FOUND), so the linker embeds it in every target.
    let mut attributes = tauri_build::Attributes::new();
    if std::env::var("CARGO_CFG_TARGET_ENV").as_deref() == Ok("msvc") {
        attributes = attributes
            .windows_attributes(tauri_build::WindowsAttributes::new_without_app_manifest());
        let manifest = std::env::current_dir()
            .expect("current dir")
            .join("windows-app-manifest.xml");
        println!("cargo:rerun-if-changed={}", manifest.display());
        println!("cargo:rustc-link-arg=/MANIFEST:EMBED");
        println!("cargo:rustc-link-arg=/MANIFESTINPUT:{}", manifest.display());
    }
    tauri_build::try_build(attributes).expect("tauri build");
}
