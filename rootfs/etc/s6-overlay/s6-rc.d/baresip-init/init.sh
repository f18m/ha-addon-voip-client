#!/usr/bin/with-contenv bashio

# ==============================================================================
# constants
# ==============================================================================

ADDON_CONFIG="/data/options.json"
BARESIP_ACCOUNTS="/etc/baresip/accounts"
BARESIP_CONTACTS="/etc/baresip/contacts"

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

log_info "Baresip accounts:"
cat -n $BARESIP_ACCOUNTS

tempio \
    -conf ${ADDON_CONFIG} \
    -template /usr/share/tempio/baresip-contacts.tmpl \
    -out "${BARESIP_CONTACTS}"

log_info "Baresip contacts:"
cat -n $BARESIP_CONTACTS

log_info "Successfully completed baresip configuration."
