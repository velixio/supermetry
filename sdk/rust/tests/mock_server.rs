//! Integration tests against a hand-rolled HTTP/1.1 mock server.

use std::collections::{HashMap, VecDeque};
use std::sync::{Arc, Mutex};
use std::time::Duration;

use supermetry::{Context, Error, Event, Supermetry};
use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio::net::TcpListener;

#[derive(Debug, Clone)]
struct Recorded {
    method: String,
    path: String,
    headers: HashMap<String, String>,
    body: serde_json::Value,
}

struct Mock {
    base: String,
    requests: Arc<Mutex<Vec<Recorded>>>,
}

/// Starts a server answering each request with the next status from
/// `statuses` (202 once the list is exhausted).
async fn mock(statuses: &[u16]) -> Mock {
    let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
    let base = format!("http://{}", listener.local_addr().unwrap());
    let requests = Arc::new(Mutex::new(Vec::new()));
    let queue = Arc::new(Mutex::new(
        statuses.iter().copied().collect::<VecDeque<_>>(),
    ));
    let recorded = requests.clone();
    tokio::spawn(async move {
        loop {
            let (mut socket, _) = listener.accept().await.unwrap();
            let recorded = recorded.clone();
            let queue = queue.clone();
            tokio::spawn(async move {
                let mut buf = Vec::new();
                let mut chunk = [0u8; 4096];
                let header_end = loop {
                    let n = socket.read(&mut chunk).await.unwrap();
                    if n == 0 {
                        return;
                    }
                    buf.extend_from_slice(&chunk[..n]);
                    if let Some(pos) = buf.windows(4).position(|w| w == b"\r\n\r\n") {
                        break pos + 4;
                    }
                };
                let head = String::from_utf8_lossy(&buf[..header_end]).to_string();
                let mut lines = head.split("\r\n");
                let mut start = lines.next().unwrap().split(' ');
                let method = start.next().unwrap().to_owned();
                let path = start.next().unwrap().to_owned();
                let headers: HashMap<String, String> = lines
                    .filter_map(|l| l.split_once(": "))
                    .map(|(k, v)| (k.to_ascii_lowercase(), v.to_owned()))
                    .collect();
                let len: usize = headers
                    .get("content-length")
                    .map(|v| v.parse().unwrap())
                    .unwrap_or(0);
                while buf.len() < header_end + len {
                    let n = socket.read(&mut chunk).await.unwrap();
                    buf.extend_from_slice(&chunk[..n]);
                }
                let body = serde_json::from_slice(&buf[header_end..header_end + len]).unwrap();
                recorded.lock().unwrap().push(Recorded {
                    method,
                    path,
                    headers,
                    body,
                });
                let status = queue.lock().unwrap().pop_front().unwrap_or(202);
                let extra = if status == 429 {
                    "Retry-After: 0\r\n"
                } else {
                    ""
                };
                let response = format!(
                    "HTTP/1.1 {status} X\r\n{extra}Content-Length: 0\r\nConnection: close\r\n\r\n"
                );
                socket.write_all(response.as_bytes()).await.unwrap();
                socket.shutdown().await.ok();
            });
        }
    });
    Mock { base, requests }
}

fn client(base: &str) -> Supermetry {
    Supermetry::builder("site_1", base)
        .url("https://supermetry.com/")
        .retry_delay(Duration::from_millis(10))
        .build()
        .unwrap()
}

#[tokio::test]
async fn track_sends_contract_payload_and_headers() {
    let server = mock(&[]).await;
    client(&server.base)
        .track(
            Event::new("Signup")
                .url("https://supermetry.com/signup?x=1")
                .prop("plan", "pro")
                .prop("seats", 3)
                .revenue("49.90", "USD")
                .profile_id("user_1")
                .user_agent("Mozilla/5.0 (Macintosh; Intel Mac OS X 14_0)")
                .ip("203.0.113.7")
                .referrer("https://news.example.com/"),
        )
        .await
        .unwrap();

    let requests = server.requests.lock().unwrap().clone();
    assert_eq!(requests.len(), 1);
    let req = &requests[0];
    assert_eq!(req.method, "POST");
    assert_eq!(req.path, "/api/public/v1/events");
    assert_eq!(req.headers["content-type"], "application/json");
    assert_eq!(
        req.headers["user-agent"],
        "Mozilla/5.0 (Macintosh; Intel Mac OS X 14_0)"
    );
    assert_eq!(req.headers["x-forwarded-for"], "203.0.113.7");
    assert_eq!(req.headers["origin"], "https://supermetry.com");
    let body = &req.body;
    assert_eq!(body["v"], 1);
    assert_eq!(body["site"], "site_1");
    assert_eq!(body["name"], "Signup");
    assert_eq!(body["url"], "https://supermetry.com/signup?x=1");
    assert_eq!(body["referrer"], "https://news.example.com/");
    assert_eq!(
        body["props"],
        serde_json::json!({"plan": "pro", "seats": 3})
    );
    assert_eq!(
        body["revenue"],
        serde_json::json!({"amount": "49.90", "currency": "USD"})
    );
    assert_eq!(body["profile_id"], "user_1");
    assert_eq!(body["id"].as_str().unwrap().len(), 32);
    assert!(body["ts"].as_i64().unwrap() > 1_700_000_000_000);
}

#[tokio::test]
async fn pageview_uses_default_url_and_own_user_agent() {
    let server = mock(&[]).await;
    client(&server.base).pageview(Context::new()).await.unwrap();
    let req = server.requests.lock().unwrap()[0].clone();
    assert_eq!(req.headers["user-agent"], "supermetry-rust/0.1.0");
    assert!(!req.headers.contains_key("x-forwarded-for"));
    assert_eq!(req.body["name"], "pageview");
    assert_eq!(req.body["url"], "https://supermetry.com/");
}

#[tokio::test]
async fn identify_sends_traits() {
    let server = mock(&[]).await;
    client(&server.base)
        .identify(
            "user_9",
            [("plan", "pro"), ("company", "Acme")],
            Context::new().ip("198.51.100.4"),
        )
        .await
        .unwrap();
    let req = server.requests.lock().unwrap()[0].clone();
    assert_eq!(req.body["name"], "identify");
    assert_eq!(req.body["profile_id"], "user_9");
    assert_eq!(
        req.body["traits"],
        serde_json::json!({"plan": "pro", "company": "Acme"})
    );
    assert_eq!(req.headers["x-forwarded-for"], "198.51.100.4");
}

#[tokio::test]
async fn retries_once_with_same_id() {
    let server = mock(&[503]).await;
    client(&server.base).track(Event::new("x")).await.unwrap();
    let requests = server.requests.lock().unwrap().clone();
    assert_eq!(requests.len(), 2);
    assert_eq!(requests[0].body["id"], requests[1].body["id"]);
    assert_eq!(requests[0].body["ts"], requests[1].body["ts"]);
}

#[tokio::test]
async fn gives_up_after_retry_budget() {
    let server = mock(&[429, 429, 429]).await;
    let err = client(&server.base)
        .track(Event::new("x"))
        .await
        .unwrap_err();
    assert!(matches!(err, Error::Status(429)));
    assert_eq!(server.requests.lock().unwrap().len(), 2);
}

#[tokio::test]
async fn bad_request_is_not_retried() {
    let server = mock(&[400]).await;
    let err = client(&server.base)
        .track(Event::new("x"))
        .await
        .unwrap_err();
    assert!(matches!(err, Error::Status(400)));
    assert_eq!(server.requests.lock().unwrap().len(), 1);
}

#[tokio::test]
async fn disabled_client_sends_nothing() {
    let server = mock(&[]).await;
    let client = Supermetry::builder("site_1", &server.base)
        .disabled(true)
        .build()
        .unwrap();
    client.track(Event::new("x")).await.unwrap();
    assert!(server.requests.lock().unwrap().is_empty());
}

#[tokio::test]
async fn network_error_surfaces_after_retry() {
    // Bind and drop a listener to get a closed port.
    let port = {
        let l = TcpListener::bind("127.0.0.1:0").await.unwrap();
        l.local_addr().unwrap().port()
    };
    let err = client(&format!("http://127.0.0.1:{port}"))
        .track(Event::new("x"))
        .await
        .unwrap_err();
    assert!(matches!(err, Error::Http(_)));
}
