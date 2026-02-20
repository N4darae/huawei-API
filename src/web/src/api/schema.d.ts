export interface paths {
  "/api/v1/healthz": {
    get: operations["healthz"];
  };
  "/api/v1/auth/login": {
    post: operations["login"];
  };
  "/api/v1/auth/logout": {
    post: operations["logout"];
  };
  "/api/v1/auth/session": {
    get: operations["session"];
  };
  "/api/v1/proxies": {
    get: operations["listProxies"];
  };
  "/api/v1/proxies/export": {
    get: operations["exportProxies"];
  };
  "/api/v1/proxies/{proxy_id}": {
    get: operations["getProxy"];
  };
  "/api/v1/proxies/{proxy_id}/rotate": {
    post: operations["rotateProxyAdmin"];
  };
  "/api/v1/proxies/{proxy_id}/auth": {
    post: operations["setProxyAuth"];
  };
  "/api/v1/proxies/{proxy_id}/auth-ips": {
    get: operations["listProxyAuthIPs"];
    post: operations["addProxyAuthIP"];
    delete: operations["deleteProxyAuthIP"];
  };
  "/api/v1/proxies/{proxy_id}/ports": {
    post: operations["setProxyPorts"];
  };
  "/api/v1/proxies/{proxy_id}/enable": {
    post: operations["setProxyEnabled"];
  };
  "/api/v1/proxies/{proxy_id}/customer": {
    post: operations["assignProxyCustomer"];
  };
  "/api/v1/proxies/{proxy_id}/selftest": {
    post: operations["selftestProxy"];
  };
  "/api/v1/slots": {
    get: operations["listSlots"];
  };
  "/api/v1/dongles": {
    get: operations["listDongles"];
  };
  "/api/v1/dongles/{dongle_id}": {
    get: operations["getDongle"];
    patch: operations["patchDongle"];
  };
  "/api/v1/dongles/{dongle_id}/reboot": {
    post: operations["rebootDongle"];
  };
  "/api/v1/dongles/{dongle_id}/netmode": {
    post: operations["setDongleNetMode"];
  };
  "/api/v1/dongles/{dongle_id}/lanip": {
    post: operations["setDongleLanIP"];
  };
  "/api/v1/dongles/{dongle_id}/sms": {
    get: operations["listSms"];
  };
  "/api/v1/dongles/{dongle_id}/sms/send": {
    post: operations["sendSms"];
  };
  "/api/v1/dongles/{dongle_id}/sms/delete": {
    post: operations["deleteSms"];
  };
  "/api/v1/dongles/{dongle_id}/sms/read": {
    post: operations["markSmsRead"];
  };
  "/api/v1/operations": {
    get: operations["listOperations"];
  };
  "/api/v1/operations/{op_id}": {
    get: operations["getOperation"];
  };
  "/api/v1/rotations": {
    get: operations["listRotations"];
  };
  "/api/v1/customers": {
    get: operations["listCustomers"];
    post: operations["createCustomer"];
  };
  "/api/v1/customers/{customer_id}": {
    patch: operations["patchCustomer"];
  };
  "/api/v1/keys": {
    get: operations["listApiKeys"];
    post: operations["createApiKey"];
  };
  "/api/v1/keys/{key_id}": {
    delete: operations["revokeApiKey"];
  };
  "/api/v1/keys/{key_id}/link-tokens": {
    post: operations["createLinkToken"];
  };
  "/api/v1/link-tokens/{token_id}": {
    delete: operations["revokeLinkToken"];
  };
  "/api/v1/events": {
    get: operations["events"];
  };
  "/api/v1/rotate/{proxy_id}": {
    post: operations["rotateProxyCustomer"];
  };
  "/api/v1/status/{proxy_id}": {
    get: operations["customerStatus"];
  };
  "/r/{link_token}": {
    get: operations["linkConfirmPage"];
    post: operations["linkRotate"];
  };
}

export type webhooks = Record<string, never>;

export interface components {
  schemas: {
    Error: {
      "error": string;
      "message": string;
      "request_id"?: string;
      "retry_after"?: number;
    };
    OpInProgress: {
      "error": "op_in_progress";
      "operation_id": string;
      "poll_url"?: string;
    };
    Health: {
      "status": "ok" | "degraded";
      "product": string;
      "node_id": string;
      "version"?: string;
      "invariants": components["schemas"]["Invariant"][];
    };
    Invariant: {
      "name": string;
      "ok": boolean;
      "detail"?: string;
    };
    LoginRequest: {
      "username": string;
      "password": string;
    };
    Session: {
      "username": string;
      "expires_at": number;
      "csrf_token"?: string;
    };
    PortRange: {
      "lo": number;
      "hi": number;
    };
    ProxyPolicy: {
      "allow_all_ports": boolean;
      "allowed_ports"?: components["schemas"]["PortRange"][];
      "max_conn": number;
      "conn_limit": number;
    };
    AuthMode: "userpass" | "iplist" | "both";
    ProxyState: "active" | "suspended" | "disabled" | "expired" | "degraded" | "unknown";
    OpKind: "rotate" | "reboot" | "set_auth" | "set_ports" | "set_lan_ip" | "set_net_mode" | "enroll" | "selftest";
    OpState: "pending" | "running" | "stalled" | "succeeded" | "failed" | "canceled";
    Trigger: "admin_ui" | "customer_api" | "auto_recovery";
    RotationResult: "changed" | "unchanged" | "failed";
    NetMode: "auto" | "2g" | "3g" | "lte";
    SmsBox: 1 | 2 | 3;
    ConnStatus: 900 | 901 | 902 | 903;
    AuthIP: {
      "id": string;
      "cidr": string;
      "note"?: string;
      "created_at": number;
    };
    AuthIPList: {
      "items": components["schemas"]["AuthIP"][];
    };
    AuthIPRequest: {
      "cidr": string;
      "note"?: string;
    };
    Proxy: {
      "id": string;
      "slot": number;
      "state": components["schemas"]["ProxyState"];
      "host": string;
      "socks_port": number;
      "http_port": number;
      "username": string;
      "password"?: string;
      "auth_mode": components["schemas"]["AuthMode"];
      "auth_ip_count"?: number;
      "enabled"?: boolean;
      "suspended"?: boolean;
      "customer_id"?: (string) | null;
      "customer_name"?: string;
      "expires_at"?: (number) | null;
      "wan_ip"?: string;
      "signal_bars"?: number;
      "data_used_bytes"?: number;
      "data_cap_bytes"?: number;
      "ports_bound": components["schemas"]["PortsBound"];
      "policy": components["schemas"]["ProxyPolicy"];
      "active_operation_id"?: (string) | null;
      "updated_at"?: number;
    };
    PortsBound: {
      "socks": boolean;
      "http": boolean;
      "probe_ok"?: boolean;
    };
    ProxyList: {
      "items": components["schemas"]["Proxy"][];
      "total"?: number;
    };
    ProxyDetail: {
      "proxy": components["schemas"]["Proxy"];
      "auth_ips"?: components["schemas"]["AuthIP"][];
      "slot"?: components["schemas"]["Slot"];
      "last_rotation"?: components["schemas"]["Rotation"];
    };
    SetAuthRequest: {
      "auth_mode": components["schemas"]["AuthMode"];
      "username"?: string;
      "password"?: string;
      "rotate_password"?: boolean;
    };
    EnableRequest: {
      "enabled": boolean;
    };
    AssignCustomerRequest: {
      "customer_id"?: (string) | null;
      "expires_at"?: (number) | null;
    };
    SelftestResult: {
      "socks_ok": boolean;
      "http_ok": boolean;
      "egress_ip"?: string;
      "latency_ms"?: number;
      "error"?: string;
    };
    Slot: {
      "id": string;
      "slot": number;
      "if_name": string;
      "usb_path": string;
      "id_path"?: string;
      "occupied": boolean;
      "dongle_id"?: (string) | null;
      "host_ip"?: string;
      "gateway_ip"?: string;
      "route_table"?: number;
    };
    SlotList: {
      "items": components["schemas"]["Slot"][];
    };
    Dongle: {
      "id": string;
      "imei": string;
      "iccid"?: string;
      "imsi"?: string;
      "firmware_ver"?: string;
      "hw_ver"?: string;
      "carrier"?: string;
      "slot": number;
      "conn_status": components["schemas"]["ConnStatus"];
      "sim_state"?: number;
      "net_mode"?: components["schemas"]["NetMode"];
      "wan_ip"?: string;
      "lan_ip_change_supported"?: boolean;
      "hilink_login_required"?: boolean;
      "auto_recover_enabled"?: boolean;
      "data_cap_bytes"?: number;
      "cap_reset_day"?: number;
      "reachable"?: boolean;
      "observed_at"?: number;
    };
    DongleList: {
      "items": components["schemas"]["Dongle"][];
    };
    DongleDetail: {
      "dongle": components["schemas"]["Dongle"];
      "signal"?: components["schemas"]["Signal"];
      "traffic"?: components["schemas"]["Traffic"];
      "slot"?: components["schemas"]["Slot"];
      "unread_sms"?: number;
    };
    DonglePatchRequest: {
      "auto_recover_enabled"?: boolean;
      "data_cap_bytes"?: number;
      "cap_reset_day"?: number;
      "carrier"?: string;
    };
    Signal: {
      "rssi"?: number;
      "rsrp"?: number;
      "rsrq"?: number;
      "sinr"?: number;
      "bars"?: number;
      "band"?: string;
      "cell_id"?: string;
      "plmn"?: string;
      "mode"?: string;
    };
    Traffic: {
      "current_upload"?: number;
      "current_download"?: number;
      "current_upload_rate"?: number;
      "current_download_rate"?: number;
      "total_upload"?: number;
      "total_download"?: number;
      "current_connect_time"?: number;
      "month_upload"?: number;
      "month_download"?: number;
    };
    NetModeRequest: {
      "net_mode": components["schemas"]["NetMode"];
    };
    LanIPRequest: {
      "gateway": string;
    };
    Sms: {
      "index": number;
      "phone": string;
      "content": string;
      "sent_at": number;
      "box": components["schemas"]["SmsBox"];
      "read": boolean;
      "sms_type"?: number;
      "is_fragment": boolean;
    };
    SmsList: {
      "items": components["schemas"]["Sms"][];
      "total": number;
    };
    SmsSendRequest: {
      "to": string[];
      "body": string;
    };
    SmsIndexRequest: {
      "index": number;
    };
    Operation: {
      "id": string;
      "kind": components["schemas"]["OpKind"];
      "subject_type": "proxy" | "dongle" | "slot" | "node";
      "subject_id": string;
      "state": components["schemas"]["OpState"];
      "step": string;
      "pct": number;
      "started_at": number;
      "deadline_at": number;
      "finished_at"?: (number) | null;
      "error"?: string;
      "result"?: { [key: string]: unknown };
      "trigger": components["schemas"]["Trigger"];
      "actor_type"?: string;
      "request_id"?: string;
    };
    OperationList: {
      "items": components["schemas"]["Operation"][];
    };
    OperationAccepted: {
      "operation_id": string;
      "poll_url": string;
      "state"?: components["schemas"]["OpState"];
      "deadline_at"?: number;
    };
    Rotation: {
      "id": string;
      "requested_at": number;
      "duration_ms": number;
      "old_public_ip"?: string;
      "new_public_ip"?: string;
      "ip_changed": boolean;
      "result": components["schemas"]["RotationResult"];
      "request_id"?: string;
    };
    RotationList: {
      "items": components["schemas"]["Rotation"][];
    };
    RotateResult: {
      "operation_id": string;
      "result": components["schemas"]["RotationResult"];
      "ip_changed": boolean;
      "old_ip"?: string;
      "new_ip"?: string;
      "duration_ms"?: number;
      "error"?: string;
    };
    CustomerStatus: {
      "proxy_id": string;
      "state": components["schemas"]["ProxyState"];
      "host": string;
      "socks_port": number;
      "http_port": number;
      "wan_ip"?: string;
      "expires_at"?: (number) | null;
      "last_rotated_at"?: number;
      "min_rotate_interval_s"?: number;
      "rotate_available_at"?: number;
    };
    Customer: {
      "id": string;
      "name": string;
      "contact"?: string;
      "note"?: string;
      "proxy_count"?: number;
      "created_at": number;
    };
    CustomerList: {
      "items": components["schemas"]["Customer"][];
    };
    CustomerRequest: {
      "name": string;
      "contact"?: string;
      "note"?: string;
    };
    ApiKey: {
      "id": string;
      "name": string;
      "prefix": string;
      "customer_id"?: (string) | null;
      "scopes": string[];
      "proxy_ids"?: string[];
      "last_used_at"?: (number) | null;
      "revoked_at"?: (number) | null;
      "created_at": number;
      "link_tokens"?: components["schemas"]["LinkToken"][];
    };
    ApiKeyList: {
      "items": components["schemas"]["ApiKey"][];
    };
    ApiKeyRequest: {
      "name": string;
      "customer_id"?: string;
      "scopes": string[];
      "proxy_ids"?: string[];
    };
    ApiKeyCreated: {
      "key": components["schemas"]["ApiKey"];
      "secret": string;
    };
    LinkToken: {
      "id": string;
      "api_key_id": string;
      "revoked_at"?: (number) | null;
      "created_at": number;
    };
    LinkTokenCreated: {
      "token": components["schemas"]["LinkToken"];
      "url": string;
    };
    HelloEvent: {
      "node_id": string;
      "server_time": number;
      "topics": string[];
      "product": string;
    };
    PatchEvent: {
      "id": string;
      "fields": { [key: string]: unknown };
    };
    SmsEvent: {
      "dongle_id": string;
      "index": number;
      "phone"?: string;
      "preview"?: string;
      "is_fragment"?: boolean;
      "sent_at"?: number;
    };
    NoticeEvent: {
      "level": "info" | "warn" | "error";
      "title": string;
      "detail"?: string;
    };
  };
}

export type $defs = Record<string, never>;

export interface operations {
  addProxyAuthIP: {
    parameters: {
      path: {
        "proxy_id": string;
      };
    };
    requestBody: {
      content: {
        "application/json": components["schemas"]["AuthIPRequest"];
      };
    };
    responses: {
      200: {
        content: {
          "application/json": components["schemas"]["AuthIPList"];
        };
      };
      400: {
        content: {
          "application/json": components["schemas"]["Error"];
        };
      };
    };
  };
  assignProxyCustomer: {
    parameters: {
      path: {
        "proxy_id": string;
      };
    };
    requestBody: {
      content: {
        "application/json": components["schemas"]["AssignCustomerRequest"];
      };
    };
    responses: {
      200: {
        content: {
          "application/json": components["schemas"]["Proxy"];
        };
      };
    };
  };
  createApiKey: {
    parameters?: never;
    requestBody: {
      content: {
        "application/json": components["schemas"]["ApiKeyRequest"];
      };
    };
    responses: {
      201: {
        content: {
          "application/json": components["schemas"]["ApiKeyCreated"];
        };
      };
    };
  };
  createCustomer: {
    parameters?: never;
    requestBody: {
      content: {
        "application/json": components["schemas"]["CustomerRequest"];
      };
    };
    responses: {
      201: {
        content: {
          "application/json": components["schemas"]["Customer"];
        };
      };
    };
  };
  createLinkToken: {
    parameters: {
      path: {
        "key_id": string;
      };
    };
    requestBody?: never;
    responses: {
      201: {
        content: {
          "application/json": components["schemas"]["LinkTokenCreated"];
        };
      };
    };
  };
  customerStatus: {
    parameters: {
      path: {
        "proxy_id": string;
      };
    };
    requestBody?: never;
    responses: {
      200: {
        content: {
          "application/json": components["schemas"]["CustomerStatus"];
        };
      };
      401: {
        content: {
          "application/json": components["schemas"]["Error"];
        };
      };
    };
  };
  deleteProxyAuthIP: {
    parameters: {
      path: {
        "proxy_id": string;
      };
    };
    requestBody: {
      content: {
        "application/json": components["schemas"]["AuthIPRequest"];
      };
    };
    responses: {
      200: {
        content: {
          "application/json": components["schemas"]["AuthIPList"];
        };
      };
      400: {
        content: {
          "application/json": components["schemas"]["Error"];
        };
      };
    };
  };
  deleteSms: {
    parameters: {
      path: {
        "dongle_id": string;
      };
    };
    requestBody: {
      content: {
        "application/json": components["schemas"]["SmsIndexRequest"];
      };
    };
    responses: {
      204: {
        content?: never;
      };
    };
  };
  events: {
    parameters: {
      query?: {
        "topics"?: string;
      };
    };
    requestBody?: never;
    responses: {
      200: {
        content: {
          "text/event-stream": string;
        };
      };
    };
  };
  exportProxies: {
    parameters: {
      query?: {
        "format"?: "txt" | "csv";
        "scheme"?: "socks5" | "http";
        "ids"?: string;
      };
    };
    requestBody?: never;
    responses: {
      200: {
        content: {
          "text/plain": string;
          "text/csv": string;
        };
      };
    };
  };
  getDongle: {
    parameters: {
      path: {
        "dongle_id": string;
      };
    };
    requestBody?: never;
    responses: {
      200: {
        content: {
          "application/json": components["schemas"]["DongleDetail"];
        };
      };
      404: {
        content: {
          "application/json": components["schemas"]["Error"];
        };
      };
    };
  };
  getOperation: {
    parameters: {
      path: {
        "op_id": string;
      };
    };
    requestBody?: never;
    responses: {
      200: {
        content: {
          "application/json": components["schemas"]["Operation"];
        };
      };
      404: {
        content: {
          "application/json": components["schemas"]["Error"];
        };
      };
    };
  };
  getProxy: {
    parameters: {
      path: {
        "proxy_id": string;
      };
    };
    requestBody?: never;
    responses: {
      200: {
        content: {
          "application/json": components["schemas"]["ProxyDetail"];
        };
      };
      404: {
        content: {
          "application/json": components["schemas"]["Error"];
        };
      };
    };
  };
  healthz: {
    parameters?: never;
    requestBody?: never;
    responses: {
      200: {
        content: {
          "application/json": components["schemas"]["Health"];
        };
      };
      503: {
        content: {
          "application/json": components["schemas"]["Health"];
        };
      };
    };
  };
  linkConfirmPage: {
    parameters: {
      path: {
        "link_token": string;
      };
    };
    requestBody?: never;
    responses: {
      200: {
        content: {
          "text/html": string;
        };
      };
      404: {
        content: {
          "application/json": components["schemas"]["Error"];
        };
      };
    };
  };
  linkRotate: {
    parameters: {
      path: {
        "link_token": string;
      };
    };
    requestBody?: never;
    responses: {
      200: {
        content: {
          "application/json": components["schemas"]["RotateResult"];
        };
      };
      202: {
        content: {
          "application/json": components["schemas"]["OperationAccepted"];
        };
      };
      409: {
        content: {
          "application/json": components["schemas"]["OpInProgress"];
        };
      };
      429: {
        content: {
          "application/json": components["schemas"]["Error"];
        };
      };
    };
  };
  listApiKeys: {
    parameters?: never;
    requestBody?: never;
    responses: {
      200: {
        content: {
          "application/json": components["schemas"]["ApiKeyList"];
        };
      };
    };
  };
  listCustomers: {
    parameters?: never;
    requestBody?: never;
    responses: {
      200: {
        content: {
          "application/json": components["schemas"]["CustomerList"];
        };
      };
    };
  };
  listDongles: {
    parameters?: never;
    requestBody?: never;
    responses: {
      200: {
        content: {
          "application/json": components["schemas"]["DongleList"];
        };
      };
    };
  };
  listOperations: {
    parameters: {
      query?: {
        "kind"?: components["schemas"]["OpKind"];
        "trigger"?: components["schemas"]["Trigger"];
        "state"?: components["schemas"]["OpState"];
        "subject_id"?: string;
        "limit"?: number;
      };
    };
    requestBody?: never;
    responses: {
      200: {
        content: {
          "application/json": components["schemas"]["OperationList"];
        };
      };
    };
  };
  listProxies: {
    parameters: {
      query?: {
        "customer_id"?: string;
        "state"?: components["schemas"]["ProxyState"];
        "expiring_within_days"?: number;
      };
    };
    requestBody?: never;
    responses: {
      200: {
        content: {
          "application/json": components["schemas"]["ProxyList"];
        };
      };
    };
  };
  listProxyAuthIPs: {
    parameters: {
      path: {
        "proxy_id": string;
      };
    };
    requestBody?: never;
    responses: {
      200: {
        content: {
          "application/json": components["schemas"]["AuthIPList"];
        };
      };
    };
  };
  listRotations: {
    parameters: {
      query?: {
        "proxy_id"?: string;
        "limit"?: number;
      };
    };
    requestBody?: never;
    responses: {
      200: {
        content: {
          "application/json": components["schemas"]["RotationList"];
        };
      };
    };
  };
  listSlots: {
    parameters?: never;
    requestBody?: never;
    responses: {
      200: {
        content: {
          "application/json": components["schemas"]["SlotList"];
        };
      };
    };
  };
  listSms: {
    parameters: {
      path: {
        "dongle_id": string;
      };
      query?: {
        "box"?: components["schemas"]["SmsBox"];
        "page"?: number;
        "size"?: number;
      };
    };
    requestBody?: never;
    responses: {
      200: {
        content: {
          "application/json": components["schemas"]["SmsList"];
        };
      };
    };
  };
  login: {
    parameters?: never;
    requestBody: {
      content: {
        "application/json": components["schemas"]["LoginRequest"];
      };
    };
    responses: {
      204: {
        content?: never;
      };
      401: {
        content: {
          "application/json": components["schemas"]["Error"];
        };
      };
      429: {
        content: {
          "application/json": components["schemas"]["Error"];
        };
      };
    };
  };
  logout: {
    parameters?: never;
    requestBody?: never;
    responses: {
      204: {
        content?: never;
      };
    };
  };
  markSmsRead: {
    parameters: {
      path: {
        "dongle_id": string;
      };
    };
    requestBody: {
      content: {
        "application/json": components["schemas"]["SmsIndexRequest"];
      };
    };
    responses: {
      204: {
        content?: never;
      };
    };
  };
  patchCustomer: {
    parameters: {
      path: {
        "customer_id": string;
      };
    };
    requestBody: {
      content: {
        "application/json": components["schemas"]["CustomerRequest"];
      };
    };
    responses: {
      200: {
        content: {
          "application/json": components["schemas"]["Customer"];
        };
      };
    };
  };
  patchDongle: {
    parameters: {
      path: {
        "dongle_id": string;
      };
    };
    requestBody: {
      content: {
        "application/json": components["schemas"]["DonglePatchRequest"];
      };
    };
    responses: {
      200: {
        content: {
          "application/json": components["schemas"]["Dongle"];
        };
      };
    };
  };
  rebootDongle: {
    parameters: {
      path: {
        "dongle_id": string;
      };
    };
    requestBody?: never;
    responses: {
      202: {
        content: {
          "application/json": components["schemas"]["OperationAccepted"];
        };
      };
      409: {
        content: {
          "application/json": components["schemas"]["OpInProgress"];
        };
      };
    };
  };
  revokeApiKey: {
    parameters: {
      path: {
        "key_id": string;
      };
    };
    requestBody?: never;
    responses: {
      204: {
        content?: never;
      };
    };
  };
  revokeLinkToken: {
    parameters: {
      path: {
        "token_id": string;
      };
    };
    requestBody?: never;
    responses: {
      204: {
        content?: never;
      };
    };
  };
  rotateProxyAdmin: {
    parameters: {
      path: {
        "proxy_id": string;
      };
    };
    requestBody?: never;
    responses: {
      202: {
        content: {
          "application/json": components["schemas"]["OperationAccepted"];
        };
      };
      409: {
        content: {
          "application/json": components["schemas"]["OpInProgress"];
        };
      };
      429: {
        content: {
          "application/json": components["schemas"]["Error"];
        };
      };
    };
  };
  rotateProxyCustomer: {
    parameters: {
      path: {
        "proxy_id": string;
      };
      query?: {
        "wait"?: boolean;
      };
    };
    requestBody?: never;
    responses: {
      200: {
        content: {
          "application/json": components["schemas"]["RotateResult"];
        };
      };
      202: {
        content: {
          "application/json": components["schemas"]["OperationAccepted"];
        };
      };
      401: {
        content: {
          "application/json": components["schemas"]["Error"];
        };
      };
      409: {
        content: {
          "application/json": components["schemas"]["OpInProgress"];
        };
      };
      429: {
        content: {
          "application/json": components["schemas"]["Error"];
        };
      };
    };
  };
  selftestProxy: {
    parameters: {
      path: {
        "proxy_id": string;
      };
    };
    requestBody?: never;
    responses: {
      200: {
        content: {
          "application/json": components["schemas"]["SelftestResult"];
        };
      };
    };
  };
  sendSms: {
    parameters: {
      path: {
        "dongle_id": string;
      };
    };
    requestBody: {
      content: {
        "application/json": components["schemas"]["SmsSendRequest"];
      };
    };
    responses: {
      204: {
        content?: never;
      };
    };
  };
  session: {
    parameters?: never;
    requestBody?: never;
    responses: {
      200: {
        content: {
          "application/json": components["schemas"]["Session"];
        };
      };
      401: {
        content: {
          "application/json": components["schemas"]["Error"];
        };
      };
    };
  };
  setDongleLanIP: {
    parameters: {
      path: {
        "dongle_id": string;
      };
    };
    requestBody: {
      content: {
        "application/json": components["schemas"]["LanIPRequest"];
      };
    };
    responses: {
      202: {
        content: {
          "application/json": components["schemas"]["OperationAccepted"];
        };
      };
      409: {
        content: {
          "application/json": components["schemas"]["OpInProgress"];
        };
      };
    };
  };
  setDongleNetMode: {
    parameters: {
      path: {
        "dongle_id": string;
      };
    };
    requestBody: {
      content: {
        "application/json": components["schemas"]["NetModeRequest"];
      };
    };
    responses: {
      202: {
        content: {
          "application/json": components["schemas"]["OperationAccepted"];
        };
      };
      409: {
        content: {
          "application/json": components["schemas"]["OpInProgress"];
        };
      };
    };
  };
  setProxyAuth: {
    parameters: {
      path: {
        "proxy_id": string;
      };
    };
    requestBody: {
      content: {
        "application/json": components["schemas"]["SetAuthRequest"];
      };
    };
    responses: {
      202: {
        content: {
          "application/json": components["schemas"]["OperationAccepted"];
        };
      };
      400: {
        content: {
          "application/json": components["schemas"]["Error"];
        };
      };
      409: {
        content: {
          "application/json": components["schemas"]["OpInProgress"];
        };
      };
    };
  };
  setProxyEnabled: {
    parameters: {
      path: {
        "proxy_id": string;
      };
    };
    requestBody: {
      content: {
        "application/json": components["schemas"]["EnableRequest"];
      };
    };
    responses: {
      200: {
        content: {
          "application/json": components["schemas"]["Proxy"];
        };
      };
    };
  };
  setProxyPorts: {
    parameters: {
      path: {
        "proxy_id": string;
      };
    };
    requestBody: {
      content: {
        "application/json": components["schemas"]["ProxyPolicy"];
      };
    };
    responses: {
      202: {
        content: {
          "application/json": components["schemas"]["OperationAccepted"];
        };
      };
      400: {
        content: {
          "application/json": components["schemas"]["Error"];
        };
      };
    };
  };
}
