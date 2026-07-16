// Copyright Built On Envoy
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

use hickory_proto::rr::Name;
use serde::Deserialize;
use std::collections::HashMap;
use std::net::IpAddr;

#[derive(Debug, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct DnsGateway {
    #[serde(default)]
    pub domains: Vec<DomainMatcher>,
    #[serde(default)]
    pub fail_open: bool,
}

/// A base address + prefix length for one address family, used by the explicit dual-stack form
/// (`ipv4` / `ipv6`).
#[derive(Debug, Default, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct FamilyRange {
    pub base_ip: String,
    #[serde(default)]
    pub prefix_len: u32,
}

/// A domain matcher. Its virtual-IP range(s) are given in one of two mutually exclusive forms:
///
/// * **Flat (single family):** `base_ip` + `prefix_len`. The address family is inferred from
///   `base_ip`; the matcher answers only that family (the other returns NODATA).
/// * **Explicit (dual-stack capable):** `ipv4` and/or `ipv6` blocks. When both are set the domain
///   is served dual-stack — `A` from `ipv4`, `AAAA` from `ipv6` — sharing this matcher's single
///   `metadata` (and matcher precedence).
#[derive(Debug, Default, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct DomainMatcher {
    pub domain: String,

    #[serde(default)]
    pub metadata: HashMap<String, String>,

    /// Flat single-family form. Mutually exclusive with `ipv4`/`ipv6`.
    #[serde(default)]
    pub base_ip: Option<String>,
    #[serde(default)]
    pub prefix_len: Option<u32>,

    /// Explicit per-family form. Either or both may be set; mutually exclusive with the flat
    /// `base_ip`/`prefix_len`.
    #[serde(default)]
    pub ipv4: Option<FamilyRange>,
    #[serde(default)]
    pub ipv6: Option<FamilyRange>,
}

impl DomainMatcher {
    /// Returns the maximum valid prefix length for an address family: 32 for IPv4, 128 for IPv6.
    pub fn max_prefix_len(addr: &IpAddr) -> u32 {
        match addr {
            IpAddr::V4(_) => 32,
            IpAddr::V6(_) => 128,
        }
    }

    /// The `(base address, prefix length)` this matcher serves for the requested family, or `None`
    /// if it does not serve that family. Assumes the config has passed [`Self::validate`].
    pub fn resolved_range(&self, want_v6: bool) -> Option<(IpAddr, u32)> {
        // Explicit form takes precedence when present.
        if self.ipv4.is_some() || self.ipv6.is_some() {
            let block = if want_v6 {
                self.ipv6.as_ref()
            } else {
                self.ipv4.as_ref()
            }?;
            let ip = block.base_ip.parse::<IpAddr>().ok()?;
            return Some((ip, block.prefix_len));
        }
        // Flat form: serves only its own family.
        let ip = self.base_ip.as_ref()?.parse::<IpAddr>().ok()?;
        (ip.is_ipv6() == want_v6).then_some((ip, self.prefix_len.unwrap_or(0)))
    }

    /// Validates the matcher at config load: the domain pattern, that exactly one range form is
    /// used, and each range's `base_ip`/`prefix_len` (with the explicit blocks required to carry
    /// an address of their own family). Returns a human-readable error on the first problem.
    pub fn validate(&self) -> Result<(), String> {
        // A bare "*" is an accepted catch-all; any other pattern must be a valid, non-bare-wildcard
        // DNS name.
        if self.domain != "*" {
            let name = Name::from_utf8(&self.domain)
                .map_err(|e| format!("invalid domain '{}': {e}", self.domain))?;
            if name.is_wildcard() && name.num_labels() < 2 {
                return Err(format!(
                    "bare wildcard '{}' is not allowed; use '*.example.com' or a bare \"*\" catch-all",
                    self.domain
                ));
            }
        }

        let has_flat = self.base_ip.is_some() || self.prefix_len.is_some();
        let has_explicit = self.ipv4.is_some() || self.ipv6.is_some();
        match (has_flat, has_explicit) {
            (false, false) => {
                return Err(format!(
                    "'{}': set either base_ip/prefix_len or ipv4/ipv6",
                    self.domain
                ))
            }
            (true, true) => {
                return Err(format!(
                    "'{}': base_ip/prefix_len is mutually exclusive with ipv4/ipv6",
                    self.domain
                ))
            }
            _ => {}
        }

        if has_flat {
            let base = self
                .base_ip
                .as_deref()
                .ok_or_else(|| format!("'{}': base_ip is required with prefix_len", self.domain))?;
            Self::validate_range(&self.domain, base, self.prefix_len, None)?;
        } else {
            if let Some(b) = &self.ipv4 {
                Self::validate_range(&self.domain, &b.base_ip, Some(b.prefix_len), Some(false))?;
            }
            if let Some(b) = &self.ipv6 {
                Self::validate_range(&self.domain, &b.base_ip, Some(b.prefix_len), Some(true))?;
            }
        }
        Ok(())
    }

    /// Validates one `base_ip`/`prefix_len` pair. `expect_v6`, when set, requires the address to be
    /// of that family — used to keep the `ipv4`/`ipv6` blocks honest.
    fn validate_range(
        domain: &str,
        base_ip: &str,
        prefix_len: Option<u32>,
        expect_v6: Option<bool>,
    ) -> Result<(), String> {
        let ip = base_ip
            .parse::<IpAddr>()
            .map_err(|e| format!("'{domain}': invalid base_ip '{base_ip}': {e}"))?;
        if let Some(want_v6) = expect_v6 {
            if ip.is_ipv6() != want_v6 {
                return Err(format!(
                    "'{domain}': {} block has an {} base_ip",
                    if want_v6 { "ipv6" } else { "ipv4" },
                    if ip.is_ipv6() { "IPv6" } else { "IPv4" }
                ));
            }
        }
        let prefix = prefix_len.ok_or_else(|| format!("'{domain}': prefix_len is required"))?;
        let max = Self::max_prefix_len(&ip);
        if !(1..=max).contains(&prefix) {
            return Err(format!(
                "'{domain}': invalid prefix_len {prefix} (must be 1..={max})"
            ));
        }
        Ok(())
    }
}
