#!/bin/sh
# Patch the image's awg-quick so WG_QUICK_FORCE_USERSPACE=1 starts the
# interface straight on the userspace engine. Upstream's fallback in add_if
# fires only when "ip link add type amneziawg" fails; a host whose kernel
# module is present but older than the config creates the interface and then
# dies in set_config with no fallback. internal/awg's QuickUp retries a
# failed start with the variable set - this script and that retry are two
# halves of one contract, so change one, change both.
set -e

target=$1
if [ -z "$target" ]; then
    echo "usage: $0 path/to/awg-quick" >&2
    exit 2
fi

# Every line to rewrite must appear exactly once as shipped: a bump of
# AWG_TOOLS_VERSION that moves upstream's text must fail the image build
# here rather than silently ship an awg-quick the patch did not touch.
for needle in \
    'add_if() {' \
    'if ! cmd ip link add "$INTERFACE" type amneziawg; then' \
    '[[ -e /sys/module/amneziawg ]]' \
    'Missing WireGuard (Amnezia VPN) kernel module.'
do
    count=$(grep -cF "$needle" "$target" || true)
    if [ "$count" != 1 ]; then
        echo "patch-awg-quick: expected one line matching: $needle (found $count)" >&2
        exit 1
    fi
done

# The cleanup trap cmd_up installs (pinned linux.bash) is never rewritten by
# this patch, but QuickUp's forced retry depends on it - issue #11: a start
# that dies half-created goes out through this trap, del_if removes the
# leftover kernel interface, and only then does WG_QUICK_FORCE_USERSPACE=1
# start clean instead of failing on "File exists". Pin it with the same
# drift-detection discipline as the lines above: a bump of AWG_TOOLS_VERSION
# that moves or drops this line must fail the image build here, not silently
# re-break the retry behind a patch that still applies.
needle="trap 'del_if; exit' INT TERM EXIT"
count=$(grep -cF "$needle" "$target" || true)
if [ "$count" != 1 ]; then
    echo "patch-awg-quick: expected one line matching: $needle (found $count)" >&2
    exit 1
fi

tmp=$(mktemp)

# In forced mode the engine must exist before anything else: upstream's own
# guard would reach "exit $ret" with ret from the "if ! cmd" condition, which
# is always 0, and report success with no interface at all.
awk -v precheck='	if [[ -n ${WG_QUICK_FORCE_USERSPACE:-} ]]; then command -v "${WG_QUICK_USERSPACE_IMPLEMENTATION:-amneziawg-go}" >/dev/null || exit 1; fi' '
    $0 == "add_if() {" { print; print precheck; next }
    { print }
' "$target" > "$tmp"

# Three line edits: skip the kernel attempt when forced; make the fallback
# guard ignore the module's presence when forced (it otherwise exits exactly
# when the old module holds); and stop calling the module "missing" when it
# is present but cannot take the config.
sed \
    -e 's@if ! cmd ip link add "$INTERFACE" type amneziawg; then@if [[ -n ${WG_QUICK_FORCE_USERSPACE:-} ]] || ! cmd ip link add "$INTERFACE" type amneziawg; then@' \
    -e 's@\[\[ -e /sys/module/amneziawg \]\]@[[ -z ${WG_QUICK_FORCE_USERSPACE:-} \&\& -e /sys/module/amneziawg ]]@' \
    -e 's@Missing WireGuard (Amnezia VPN) kernel module\.@Kernel module unavailable or incompatible.@' \
    "$tmp" > "$tmp.new"
cat "$tmp.new" > "$target"
rm -f "$tmp" "$tmp.new"

for needle in \
    '	if [[ -n ${WG_QUICK_FORCE_USERSPACE:-} ]]; then command -v "${WG_QUICK_USERSPACE_IMPLEMENTATION:-amneziawg-go}" >/dev/null || exit 1; fi' \
    'if [[ -n ${WG_QUICK_FORCE_USERSPACE:-} ]] || ! cmd ip link add "$INTERFACE" type amneziawg; then' \
    '[[ -z ${WG_QUICK_FORCE_USERSPACE:-} && -e /sys/module/amneziawg ]]' \
    'Kernel module unavailable or incompatible.' \
    "trap 'del_if; exit' INT TERM EXIT"
do
    if ! grep -qF "$needle" "$target"; then
        echo "patch-awg-quick: patched file is missing: $needle" >&2
        exit 1
    fi
done
