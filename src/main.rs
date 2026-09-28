use std::{collections::HashMap, net::SocketAddr, path::PathBuf, sync::Arc};

use axum::{
    body::Body,
    extract::{Path, Query, State},
    http::{header, HeaderValue, StatusCode},
    response::{Html, IntoResponse},
    routing::{delete, get},
    Json, Router,
};
use clap::Parser;
use serde::{Deserialize, Serialize};
use tokio::sync::RwLock;
use tracing_subscriber::EnvFilter;
use zookeeper_client::Client;

#[derive(Parser)]
#[command(
    name = "zookeeper-explorer",
    about = "A web UI for browsing ZooKeeper nodes"
)]
struct Args {
    /// TOML configuration file
    #[arg(short, long, default_value = "config.toml")]
    config: PathBuf,
    /// Web server bind address (overrides config)
    #[arg(long)]
    listen: Option<SocketAddr>,
    /// ZooKeeper ensemble, repeatable (overrides configured clusters)
    #[arg(long = "zk", value_name = "NAME=HOSTS")]
    clusters: Vec<String>,
}

#[derive(Debug, Clone, Deserialize, Serialize)]
struct Cluster {
    name: String,
    address: String,
}

#[derive(Debug, Deserialize)]
struct Config {
    #[serde(default = "default_listen")]
    listen: SocketAddr,
    #[serde(default)]
    clusters: Vec<Cluster>,
}

fn default_listen() -> SocketAddr {
    "127.0.0.1:8080".parse().expect("valid default address")
}

#[derive(Clone)]
struct AppState {
    clusters: Vec<Cluster>,
    clients: Arc<RwLock<HashMap<usize, Client>>>,
}

#[derive(Serialize)]
struct NodeEntry {
    name: String,
    path: String,
    is_dir: bool,
    data_len: i32,
    children_count: i32,
}

#[derive(Serialize)]
struct NodeResponse {
    path: String,
    nodes: Vec<NodeEntry>,
    data: Option<String>,
    data_base64: Option<String>,
    data_len: i32,
}

#[tokio::main]
async fn main() -> anyhowless::Result<()> {
    tracing_subscriber::fmt()
        .with_env_filter(
            EnvFilter::try_from_default_env().unwrap_or_else(|_| EnvFilter::new("info")),
        )
        .init();
    let args = Args::parse();
    let mut config = if args.config.exists() {
        let raw = std::fs::read_to_string(&args.config)?;
        toml::from_str::<Config>(&raw)?
    } else {
        Config {
            listen: default_listen(),
            clusters: Vec::new(),
        }
    };
    if let Some(listen) = args.listen {
        config.listen = listen;
    }
    if !args.clusters.is_empty() {
        config.clusters = args
            .clusters
            .into_iter()
            .map(|item| {
                let (name, address) = item.split_once('=').unwrap_or((&item, &item));
                Cluster {
                    name: name.to_string(),
                    address: address.to_string(),
                }
            })
            .collect();
    }
    let state = AppState {
        clusters: config.clusters,
        clients: Arc::new(RwLock::new(HashMap::new())),
    };
    let app = Router::new()
        .route("/", get(index))
        .route("/api/clusters", get(list_clusters))
        .route("/api/clusters/{id}/nodes", get(list_nodes))
        .route("/api/clusters/{id}/node/download", get(download_node))
        .route("/api/clusters/{id}/nodes", delete(delete_node))
        .with_state(state);
    let listener = tokio::net::TcpListener::bind(config.listen).await?;
    tracing::info!(address = %config.listen, "ZooKeeper Explorer is ready");
    axum::serve(listener, app).await?;
    Ok(())
}

// A tiny local error wrapper keeps startup errors readable without pulling in another dependency.
mod anyhowless {
    pub type Result<T> = std::result::Result<T, Box<dyn std::error::Error + Send + Sync>>;
}

async fn index() -> Html<&'static str> {
    Html(include_str!("../static/index.html"))
}

async fn list_clusters(State(state): State<AppState>) -> Json<Vec<Cluster>> {
    Json(state.clusters.clone())
}

async fn list_nodes(
    State(state): State<AppState>,
    Path(id): Path<usize>,
    Query(query): Query<HashMap<String, String>>,
) -> Result<Json<NodeResponse>, (StatusCode, String)> {
    let cluster = state
        .clusters
        .get(id)
        .ok_or((StatusCode::NOT_FOUND, "Unknown cluster".into()))?;
    let path = normalize_path(query.get("path").map(String::as_str).unwrap_or("/"))?;

    let client = {
        let mut clients = state.clients.write().await;
        if let Some(client) = clients.get(&id) {
            client.clone()
        } else {
            let client = Client::connect(&cluster.address).await.map_err(|error| {
                (
                    StatusCode::BAD_GATEWAY,
                    format!("ZooKeeper connection failed: {error}"),
                )
            })?;
            clients.insert(id, client.clone());
            client
        }
    };

    let (children, _) = client.get_children(&path).await.map_err(|error| {
        (
            StatusCode::BAD_GATEWAY,
            format!("Could not list {path}: {error}"),
        )
    })?;
    let mut nodes = Vec::with_capacity(children.len());
    for name in children {
        let child_path = if path == "/" {
            format!("/{name}")
        } else {
            format!("{path}/{name}")
        };
        let (grandchildren, stat) = client.get_children(&child_path).await.map_err(|error| {
            (
                StatusCode::BAD_GATEWAY,
                format!("Could not inspect {child_path}: {error}"),
            )
        })?;
        nodes.push(NodeEntry {
            name,
            path: child_path,
            is_dir: !grandchildren.is_empty(),
            data_len: stat.data_length,
            children_count: grandchildren.len() as i32,
        });
    }
    nodes.sort_by(|a, b| {
        b.is_dir
            .cmp(&a.is_dir)
            .then_with(|| a.name.to_lowercase().cmp(&b.name.to_lowercase()))
    });

    let (data, data_base64, data_len) = if path == "/" {
        (None, None, 0)
    } else {
        let (bytes, stat) = client.get_data(&path).await.map_err(|error| {
            (
                StatusCode::BAD_GATEWAY,
                format!("Could not read {path}: {error}"),
            )
        })?;
        let text = String::from_utf8(bytes.clone()).ok();
        let encoded = if text.is_none() {
            Some(base64(&bytes))
        } else {
            None
        };
        (text, encoded, stat.data_length)
    };
    Ok(Json(NodeResponse {
        path,
        nodes,
        data,
        data_base64,
        data_len,
    }))
}

async fn download_node(
    State(state): State<AppState>,
    Path(id): Path<usize>,
    Query(query): Query<HashMap<String, String>>,
) -> Result<axum::response::Response, (StatusCode, String)> {
    let path = normalize_path(query.get("path").map(String::as_str).unwrap_or("/"))?;
    if path == "/" {
        return Err((
            StatusCode::BAD_REQUEST,
            "Cannot download the root node".into(),
        ));
    }
    let client = get_client(&state, id).await?;
    let (bytes, _) = client.get_data(&path).await.map_err(|error| {
        (
            StatusCode::BAD_GATEWAY,
            format!("Could not read {path}: {error}"),
        )
    })?;
    let name = path.rsplit('/').next().unwrap_or("node");
    let disposition = format!(
        "attachment; filename*=UTF-8''{}",
        encode_header_filename(name)
    );
    let mut response = Body::from(bytes).into_response();
    response.headers_mut().insert(
        header::CONTENT_TYPE,
        HeaderValue::from_static("application/octet-stream"),
    );
    response.headers_mut().insert(
        header::CONTENT_DISPOSITION,
        HeaderValue::from_str(&disposition)
            .map_err(|_| (StatusCode::BAD_REQUEST, "Invalid node name".into()))?,
    );
    Ok(response)
}

#[derive(Serialize)]
struct DeleteResponse {
    deleted_nodes: usize,
}

async fn delete_node(
    State(state): State<AppState>,
    Path(id): Path<usize>,
    Query(query): Query<HashMap<String, String>>,
) -> Result<Json<DeleteResponse>, (StatusCode, String)> {
    let path = normalize_path(query.get("path").map(String::as_str).unwrap_or("/"))?;
    if path == "/" {
        return Err((
            StatusCode::BAD_REQUEST,
            "Cannot delete the root node".into(),
        ));
    }
    let client = get_client(&state, id).await?;
    let mut stack = vec![(path, false)];
    let mut deleted_nodes = 0;
    while let Some((node_path, visited)) = stack.pop() {
        if visited {
            client.delete(&node_path, None).await.map_err(|error| {
                (
                    StatusCode::CONFLICT,
                    format!("Recursive delete stopped at {node_path}: {error}; {deleted_nodes} child node(s) already deleted"),
                )
            })?;
            deleted_nodes += 1;
            continue;
        }
        let (children, _) = client.get_children(&node_path).await.map_err(|error| {
            (
                StatusCode::BAD_GATEWAY,
                format!("Could not inspect {node_path} for recursive deletion: {error}; {deleted_nodes} child node(s) already deleted"),
            )
        })?;
        stack.push((node_path.clone(), true));
        for child in children {
            stack.push((format!("{node_path}/{child}"), false));
        }
    }
    Ok(Json(DeleteResponse { deleted_nodes }))
}

async fn get_client(state: &AppState, id: usize) -> Result<Client, (StatusCode, String)> {
    let cluster = state
        .clusters
        .get(id)
        .ok_or((StatusCode::NOT_FOUND, "Unknown cluster".into()))?;
    let mut clients = state.clients.write().await;
    if let Some(client) = clients.get(&id) {
        return Ok(client.clone());
    }
    let client = Client::connect(&cluster.address).await.map_err(|error| {
        (
            StatusCode::BAD_GATEWAY,
            format!("ZooKeeper connection failed: {error}"),
        )
    })?;
    clients.insert(id, client.clone());
    Ok(client)
}

fn encode_header_filename(name: &str) -> String {
    let mut encoded = String::with_capacity(name.len());
    for byte in name.bytes() {
        if byte.is_ascii_alphanumeric() || matches!(byte, b'-' | b'_' | b'.' | b'~') {
            encoded.push(byte as char);
        } else {
            encoded.push_str(&format!("%{byte:02X}"));
        }
    }
    encoded
}

fn normalize_path(path: &str) -> Result<String, (StatusCode, String)> {
    if !path.starts_with('/')
        || path.contains("//")
        || path.split('/').any(|part| part == ".." || part == ".")
    {
        return Err((StatusCode::BAD_REQUEST, "Invalid ZooKeeper path".into()));
    }
    Ok(if path.len() > 1 {
        path.trim_end_matches('/').to_string()
    } else {
        "/".into()
    })
}

fn base64(bytes: &[u8]) -> String {
    const TABLE: &[u8; 64] = b"ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";
    let mut out = String::with_capacity(bytes.len().div_ceil(3) * 4);
    for chunk in bytes.chunks(3) {
        let a = chunk[0] as usize;
        let b = *chunk.get(1).unwrap_or(&0) as usize;
        let c = *chunk.get(2).unwrap_or(&0) as usize;
        out.push(TABLE[a >> 2] as char);
        out.push(TABLE[((a & 3) << 4) | (b >> 4)] as char);
        out.push(if chunk.len() > 1 {
            TABLE[((b & 15) << 2) | (c >> 6)] as char
        } else {
            '='
        });
        out.push(if chunk.len() > 2 {
            TABLE[c & 63] as char
        } else {
            '='
        });
    }
    out
}

impl IntoResponse for NodeResponse {
    fn into_response(self) -> axum::response::Response {
        Json(self).into_response()
    }
}
