// Lane links open one Sessions lane in this app:
//
//     sessions://lane/<session-id>[?machine=<machine-id>]
//
// The CLI prints them (runtime/internal/lanelink is the Go definition this
// mirrors). A link names a lane and nothing else: it is parsed into two
// UUID-shaped identifiers and handed to the viewer, which opens the session the
// same way selecting it in the navigator does. Nothing in the URL is executed,
// fetched, or passed to a shell. Pairing links share the scheme under the
// `pair` host and are not lane links.

use serde::Serialize;
use std::sync::Mutex;

pub const LANE_LINK_SCHEME: &str = "sessions";
const LANE_LINK_HOST: &str = "lane";
// The viewer listens for this and then takes the pending link, so a link that
// arrives before the page is ready is still opened once it is.
pub const LANE_LINK_EVENT: &str = "lane-link";

#[derive(Clone, Debug, PartialEq, Eq, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct LaneLink {
    pub session_id: String,
    pub machine_id: Option<String>,
}

// The most recent lane link the viewer has not taken yet. Only the latest is
// kept: opening two links quickly should land on the second.
#[derive(Default)]
pub struct PendingLaneLink(Mutex<Option<LaneLink>>);

impl PendingLaneLink {
    pub fn replace(&self, link: LaneLink) {
        if let Ok(mut pending) = self.0.lock() {
            *pending = Some(link);
        }
    }

    pub fn take(&self) -> Option<LaneLink> {
        self.0.lock().ok().and_then(|mut pending| pending.take())
    }
}

// parse_lane_link accepts exactly a lane link with UUID identifiers and
// refuses everything else, including a lane link carrying any other query
// parameter, credentials, a port, a fragment, or extra path segments.
pub fn parse_lane_link(raw: &str) -> Option<LaneLink> {
    let parsed = url::Url::parse(raw.trim()).ok()?;
    if !parsed.scheme().eq_ignore_ascii_case(LANE_LINK_SCHEME)
        || !parsed
            .host_str()
            .is_some_and(|host| host.eq_ignore_ascii_case(LANE_LINK_HOST))
        || !parsed.username().is_empty()
        || parsed.password().is_some()
        || parsed.port().is_some()
        || parsed.fragment().is_some()
    {
        return None;
    }
    let session_id = parsed.path().strip_prefix('/')?;
    if !valid_id(session_id) {
        return None;
    }
    let mut machine_id = None;
    for (key, value) in parsed.query_pairs() {
        if key != "machine" || machine_id.is_some() || !valid_id(&value) {
            return None;
        }
        machine_id = Some(value.to_ascii_lowercase());
    }
    Some(LaneLink {
        session_id: session_id.to_ascii_lowercase(),
        machine_id,
    })
}

// valid_id is the canonical 8-4-4-4-12 hexadecimal UUID shape session and
// machine identities use.
pub fn valid_id(value: &str) -> bool {
    value.len() == 36
        && value.bytes().enumerate().all(|(index, byte)| match index {
            8 | 13 | 18 | 23 => byte == b'-',
            _ => byte.is_ascii_hexdigit(),
        })
}

#[cfg(test)]
mod tests {
    use super::*;

    const SESSION: &str = "31000000-0000-4000-8000-0000000000a1";
    const MACHINE: &str = "9b2f0c3e-1d4a-4e5b-8c6d-7e8f9a0b1c2d";

    #[test]
    fn parses_lane_links_with_and_without_machine() {
        assert_eq!(
            parse_lane_link(&format!("sessions://lane/{SESSION}")),
            Some(LaneLink {
                session_id: SESSION.to_string(),
                machine_id: None
            })
        );
        assert_eq!(
            parse_lane_link(&format!(" sessions://lane/{SESSION}?machine={MACHINE}\n")),
            Some(LaneLink {
                session_id: SESSION.to_string(),
                machine_id: Some(MACHINE.to_string())
            })
        );
        assert_eq!(
            parse_lane_link(&format!(
                "SESSIONS://LANE/{}?machine={}",
                SESSION.to_uppercase(),
                MACHINE.to_uppercase()
            )),
            Some(LaneLink {
                session_id: SESSION.to_string(),
                machine_id: Some(MACHINE.to_string())
            })
        );
    }

    #[test]
    fn refuses_anything_that_is_not_exactly_a_lane_link() {
        for raw in [
            "sessions://pair?host=http%3A%2F%2F192.168.1.25%3A8787&t=ticket-id.ticket-secret"
                .to_string(),
            format!("https://lane/{SESSION}"),
            format!("sessions://lane/{SESSION}/extra"),
            format!("sessions://lane/{SESSION}?machine=not-a-machine"),
            format!("sessions://lane/{SESSION}?machine={MACHINE}&machine={MACHINE}"),
            format!("sessions://lane/{SESSION}?cmd=rm%20-rf"),
            format!("sessions://lane/{SESSION}#fragment"),
            format!("sessions://user:secret@lane/{SESSION}"),
            format!("sessions://lane:8787/{SESSION}"),
            "sessions://lane/".to_string(),
            "sessions://lane/../../etc/passwd".to_string(),
            "sessions://lane/31000000-0000-4000-8000-0000000000g1".to_string(),
            "sessions://lane/31000000000040008000-0000000000a1-".to_string(),
            "javascript:alert(1)".to_string(),
            "".to_string(),
        ] {
            assert_eq!(parse_lane_link(&raw), None, "{raw}");
        }
    }

    #[test]
    fn pending_link_keeps_only_the_latest_until_taken() {
        let pending = PendingLaneLink::default();
        assert_eq!(pending.take(), None);
        pending.replace(parse_lane_link(&format!("sessions://lane/{SESSION}")).unwrap());
        let latest =
            parse_lane_link(&format!("sessions://lane/{SESSION}?machine={MACHINE}")).unwrap();
        pending.replace(latest.clone());
        assert_eq!(pending.take(), Some(latest));
        assert_eq!(pending.take(), None);
    }
}
