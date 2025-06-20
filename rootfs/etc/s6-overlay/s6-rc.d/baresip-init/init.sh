#!/usr/bin/with-contenv bashio

# ==============================================================================
# constants
# ==============================================================================

ADDON_CONFIG="/data/options.json"
BARESIP_ACCOUNTS="/etc/baresip/accounts"

# ==============================================================================
# FUNCTIONS
# ==============================================================================

function log_info() {
    bashio::log.info "baresip-init.sh: $@"
}

log_info "Configuring baresip..."
tempio \
    -conf ${ADDON_CONFIG} \
    -template /usr/share/tempio/baresip-accounts.tmpl \
    -out "${BARESIP_ACCOUNTS}"

log_info "Full baresip config:"
cat -n $BARESIP_CONFIG

log_info "Successfully completed baresip configuration."
