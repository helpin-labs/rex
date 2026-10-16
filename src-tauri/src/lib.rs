use std::net::{SocketAddr, TcpListener, TcpStream};
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::Mutex;
use std::time::Duration;

use serde::Serialize;
use tauri::menu::{MenuBuilder, MenuItemBuilder};
use tauri::tray::{MouseButton, MouseButtonState, TrayIconBuilder};
use tauri::path::BaseDirectory;
use tauri::{Emitter, Manager, RunEvent, WindowEvent};
use tauri_plugin_shell::process::{CommandChild, CommandEvent};
use tauri_plugin_shell::ShellExt;

/// The supervised rex-host sidecar. Dropping the child closes its stdin,
/// which rex-host treats as "the desktop app is gone": it stops the agent
/// runtime and exits, including when this process crashes.
struct Host {
    child: Mutex<Option<CommandChild>>,
    connection: Connection,
}

#[derive(Clone, Serialize)]
struct Connection {
    url: String,
    token: String,
}

/// Returns the host URL and per-launch token once the host accepts
/// connections, or null while it is still starting.
#[tauri::command]
fn host_connection(state: tauri::State<'_, Host>) -> Option<Connection> {
    let address: SocketAddr = state.connection.url.trim_start_matches("http://").parse().ok()?;
    TcpStream::connect_timeout(&address, Duration::from_millis(200)).ok()?;
    Some(state.connection.clone())
}

/// Shows how many conversations wait for the user: a Dock badge and a
/// count next to the menu bar icon. Zero clears both.
#[tauri::command]
fn set_attention(app: tauri::AppHandle, count: u32) {
    let shown = (count > 0).then_some(count);
    if let Some(window) = app.get_webview_window("main") {
        let _ = window.set_badge_count(shown.map(i64::from));
    }
    if let Some(tray) = app.tray_by_id("rex") {
        let _ = tray.set_title(shown.map(|count| count.to_string()));
    }
}

/// Whether the Rex window is in front, from its focus events.
static FOCUSED: AtomicBool = AtomicBool::new(true);

/// Posts a system notification unless Rex is in front. Clicking it brings
/// Rex forward and tells the window which conversation to open.
#[tauri::command]
fn notify(app: tauri::AppHandle, title: String, body: String, conversation_id: String) {
    if FOCUSED.load(Ordering::Relaxed) {
        return;
    }
    #[cfg(target_os = "macos")]
    std::thread::spawn(move || {
        static APPLICATION: std::sync::Once = std::sync::Once::new();
        APPLICATION.call_once(|| {
            let _ = mac_notification_sys::set_application(&app.config().identifier);
        });
        let response = mac_notification_sys::Notification::new()
            .title(&title)
            .message(&body)
            .wait_for_click(true)
            .send();
        if let Ok(mac_notification_sys::NotificationResponse::Click) = response {
            reveal(&app);
            let _ = app.emit("open-conversation", conversation_id);
        }
    });
    #[cfg(not(target_os = "macos"))]
    let _ = (app, title, body, conversation_id);
}

fn random_token() -> String {
    let mut bytes = [0u8; 32];
    getrandom::getrandom(&mut bytes).expect("system randomness");
    bytes.iter().map(|byte| format!("{byte:02x}")).collect()
}

fn free_port() -> u16 {
    TcpListener::bind("127.0.0.1:0")
        .and_then(|listener| listener.local_addr())
        .map(|address| address.port())
        .unwrap_or(18797)
}

/// The host keeps the same port across launches: runs store the host's
/// address for their memory tools, so a run continued after a restart must
/// find the host where it was. The last launch's address is in
/// connection.json; a new port is picked only if that one is taken.
fn host_port(data_dir: &std::path::Path) -> u16 {
    let saved = std::fs::read_to_string(data_dir.join("connection.json"))
        .ok()
        .and_then(|text| serde_json::from_str::<serde_json::Value>(&text).ok())
        .and_then(|value| value["url"].as_str().and_then(|url| url.rsplit(':').next()).and_then(|port| port.parse::<u16>().ok()));
    match saved {
        Some(port) if TcpListener::bind(("127.0.0.1", port)).is_ok() => port,
        _ => free_port(),
    }
}

fn stop_host(handle: &tauri::AppHandle) {
    if let Some(host) = handle.try_state::<Host>() {
        if let Ok(mut child) = host.child.lock() {
            let _ = child.take();
        }
    }
}

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    let app = tauri::Builder::default()
        .plugin(tauri_plugin_opener::init())
        .plugin(tauri_plugin_shell::init())
        .invoke_handler(tauri::generate_handler![host_connection, set_attention, notify])
        .on_window_event(|window, event| {
            if let WindowEvent::Focused(focused) = event {
                FOCUSED.store(*focused, Ordering::Relaxed);
            }
            // Closing the window keeps Rex in the menu bar so routines keep
            // running. Quit from the menu bar icon stops everything.
            if let WindowEvent::CloseRequested { api, .. } = event {
                if window.label() == "main" {
                    api.prevent_close();
                    let _ = window.hide();
                }
            }
        })
        .setup(|app| {
            #[cfg(target_os = "windows")]
            let data_dir = app.path().app_local_data_dir()?;
            #[cfg(not(target_os = "windows"))]
            let data_dir = app.path().app_data_dir()?;
            std::fs::create_dir_all(&data_dir)?;
            let port = host_port(&data_dir);
            let connection = Connection { url: format!("http://127.0.0.1:{port}"), token: random_token() };

            let embedding_model = app
                .path()
                .resolve("resources/models/bekko-embedding-v1-a8m-Q8_0.gguf", BaseDirectory::Resource)
                .map(|path| path.to_string_lossy().to_string())
                .unwrap_or_default();
            let (mut events, child) = app
                .shell()
                .sidecar("rex-host")?
                .env("REX_EMBEDDING_MODEL", embedding_model)
                .env("REX_DATA_DIR", data_dir.to_string_lossy().to_string())
                .env("REX_HOST_ADDR", format!("127.0.0.1:{port}"))
                .env("REX_UI_TOKEN", connection.token.clone())
                .env("REX_WATCH_STDIN", "1")
                .spawn()?;
            app.manage(Host { child: Mutex::new(Some(child)), connection });

            tauri::async_runtime::spawn(async move {
                while let Some(event) = events.recv().await {
                    match event {
                        CommandEvent::Stderr(bytes) | CommandEvent::Stdout(bytes) => {
                            eprint!("[rex-host] {}", String::from_utf8_lossy(&bytes));
                        }
                        CommandEvent::Terminated(status) => {
                            eprintln!("[rex-host] exited: {:?}", status.code);
                        }
                        _ => {}
                    }
                }
            });

            let open = MenuItemBuilder::with_id("open", "Open Rex").build(app)?;
            let quit = MenuItemBuilder::with_id("quit", "Quit Rex").accelerator("Cmd+Q").build(app)?;
            let menu = MenuBuilder::new(app).item(&open).separator().item(&quit).build()?;
            TrayIconBuilder::with_id("rex")
                // The logo's face as a template image: macOS draws it in the
                // menu bar's colour, so it flips with light and dark mode.
                .icon(tauri::image::Image::from_bytes(include_bytes!("../icons/tray.png"))?)
                .icon_as_template(true)
                .tooltip("Rex")
                .menu(&menu)
                .show_menu_on_left_click(false)
                .on_menu_event(|app, event| match event.id().as_ref() {
                    "open" => reveal(app),
                    "quit" => {
                        stop_host(app);
                        app.exit(0);
                    }
                    _ => {}
                })
                .on_tray_icon_event(|tray, event| {
                    if let tauri::tray::TrayIconEvent::Click { button: MouseButton::Left, button_state: MouseButtonState::Up, .. } = event {
                        reveal(tray.app_handle());
                    }
                })
                .build(app)?;
            Ok(())
        })
        .build(tauri::generate_context!())
        .expect("error building Rex");

    app.run(|handle, event| match event {
        RunEvent::Reopen { .. } => reveal(handle),
        RunEvent::Exit => stop_host(handle),
        _ => {}
    });
}

fn reveal(app: &tauri::AppHandle) {
    if let Some(window) = app.get_webview_window("main") {
        let _ = window.unminimize();
        let _ = window.show();
        let _ = window.set_focus();
    }
}
