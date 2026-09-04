#!/bin/sh

mkdir -p /var/log/amnezia
chmod 755 /var/log/amnezia

lsmod | grep -E "^nf_tables|^nft_"
nft_true=$?

if [ "$nft_true" -ne 0 ]; then
    # Shadow the nft-backed iptables with the legacy one. The binaries live in
    # /sbin on older Alpine and in /usr/sbin since the /usr merge, so resolve
    # them through PATH and drop the symlink in /usr/local/sbin, which comes
    # first in PATH either way.
    legacy=$(command -v iptables-legacy)
    if [ -n "$legacy" ]; then
        mkdir -p /usr/local/sbin
        ln -sf "$legacy" /usr/local/sbin/iptables
        echo "iptables-legacy set as default"
    else
        echo "iptables-legacy not found, keeping the nft backend" >&2
    fi
fi

exec /usr/bin/api
