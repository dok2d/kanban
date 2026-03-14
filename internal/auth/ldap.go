package auth

import (
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"net"
	"strings"
	"time"
)

// LDAPConfig holds LDAP/Active Directory connection settings.
type LDAPConfig struct {
	Host       string // e.g. "ldap.example.com"
	Port       int    // 389 for LDAP, 636 for LDAPS
	UseTLS     bool   // true for LDAPS (port 636)
	StartTLS   bool   // true for StartTLS on port 389
	SkipVerify bool   // skip TLS certificate verification

	// Bind credentials for searching users
	BindDN       string // e.g. "cn=admin,dc=example,dc=com"
	BindPassword string

	// Search settings
	BaseDN       string // e.g. "dc=example,dc=com"
	UserFilter   string // e.g. "(&(objectClass=user)(sAMAccountName=%s))" — %s is replaced with username
	UsernameAttr string // attribute for username, e.g. "sAMAccountName" or "uid"

	// Role mapping (optional)
	DefaultRole string // "regular" or "readonly"
	AdminGroup  string // DN of admin group, e.g. "cn=kanban-admins,ou=groups,dc=example,dc=com"
	MemberAttr  string // attribute in group for membership, e.g. "member"
}

// LDAPResult contains the result of LDAP authentication.
type LDAPResult struct {
	Username string
	DN       string
	IsAdmin  bool
}

// LDAPAuthenticate performs LDAP bind authentication.
// It connects to the LDAP server, binds with service credentials,
// searches for the user, then attempts to bind as that user.
func LDAPAuthenticate(cfg *LDAPConfig, username, password string) (*LDAPResult, error) {
	if username == "" || password == "" {
		return nil, fmt.Errorf("empty credentials")
	}

	conn, err := ldapConnect(cfg)
	if err != nil {
		return nil, fmt.Errorf("ldap connect: %w", err)
	}
	defer conn.Close()

	msgID := 1

	// Step 1: Bind with service account
	if cfg.BindDN != "" {
		if err := ldapBind(conn, &msgID, cfg.BindDN, cfg.BindPassword); err != nil {
			return nil, fmt.Errorf("service bind: %w", err)
		}
	}

	// Step 2: Search for user
	filter := strings.ReplaceAll(cfg.UserFilter, "%s", ldapEscapeFilter(username))
	userDN, foundUsername, err := ldapSearchUser(conn, &msgID, cfg.BaseDN, filter, cfg.UsernameAttr)
	if err != nil {
		return nil, fmt.Errorf("user search: %w", err)
	}
	if userDN == "" {
		return nil, fmt.Errorf("user not found")
	}

	// Use the username from LDAP if found, otherwise use the input
	if foundUsername == "" {
		foundUsername = username
	}

	// Step 3: Bind as the found user to verify password
	// Need a new connection for user bind
	conn2, err := ldapConnect(cfg)
	if err != nil {
		return nil, fmt.Errorf("ldap reconnect: %w", err)
	}
	defer conn2.Close()

	msgID2 := 1
	if err := ldapBind(conn2, &msgID2, userDN, password); err != nil {
		return nil, fmt.Errorf("invalid credentials")
	}

	result := &LDAPResult{
		Username: foundUsername,
		DN:       userDN,
	}

	// Step 4: Check admin group membership (optional)
	if cfg.AdminGroup != "" && cfg.MemberAttr != "" {
		result.IsAdmin = ldapCheckGroupMembership(conn, &msgID, cfg.AdminGroup, cfg.MemberAttr, userDN)
	}

	return result, nil
}

// --- LDAP protocol implementation (minimal BER/LDAP over TCP) ---

func ldapConnect(cfg *LDAPConfig) (net.Conn, error) {
	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	timeout := 10 * time.Second

	if cfg.UseTLS {
		return tls.DialWithDialer(&net.Dialer{Timeout: timeout}, "tcp", addr, &tls.Config{
			InsecureSkipVerify: cfg.SkipVerify,
			ServerName:         cfg.Host,
		})
	}

	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return nil, err
	}

	if cfg.StartTLS {
		// Send StartTLS extended request
		tlsConn, err := ldapStartTLS(conn, cfg)
		if err != nil {
			conn.Close()
			return nil, err
		}
		return tlsConn, nil
	}

	return conn, nil
}

func ldapStartTLS(conn net.Conn, cfg *LDAPConfig) (net.Conn, error) {
	// Extended request for StartTLS OID: 1.3.6.1.4.1.1466.20037
	oid := "1.3.6.1.4.1.1466.20037"
	extReq := berSequence(
		berInteger(1), // messageID
		berConstructed(0x77, // ExtendedRequest [APPLICATION 23]
			berOctetStringWithTag(0x80, []byte(oid)),
		),
	)
	if _, err := conn.Write(extReq); err != nil {
		return nil, err
	}
	resp, err := berReadMessage(conn)
	if err != nil {
		return nil, fmt.Errorf("starttls response: %w", err)
	}
	if rc := berExtractResultCode(resp); rc != 0 {
		return nil, fmt.Errorf("starttls failed: result code %d", rc)
	}

	// Upgrade to TLS
	tlsConn := tls.Client(conn, &tls.Config{
		InsecureSkipVerify: cfg.SkipVerify,
		ServerName:         cfg.Host,
	})
	if err := tlsConn.Handshake(); err != nil {
		return nil, fmt.Errorf("tls handshake: %w", err)
	}
	return tlsConn, nil
}

func ldapBind(conn net.Conn, msgID *int, dn, password string) error {
	*msgID++
	req := berSequence(
		berInteger(*msgID),
		berConstructed(0x60, // BindRequest [APPLICATION 0]
			berInteger(3), // version
			berOctetString([]byte(dn)),
			berOctetStringWithTag(0x80, []byte(password)), // simple auth
		),
	)
	if _, err := conn.Write(req); err != nil {
		return err
	}
	resp, err := berReadMessage(conn)
	if err != nil {
		return fmt.Errorf("bind response: %w", err)
	}
	if rc := berExtractResultCode(resp); rc != 0 {
		return fmt.Errorf("bind failed: result code %d", rc)
	}
	return nil
}

func ldapSearchUser(conn net.Conn, msgID *int, baseDN, filter, usernameAttr string) (string, string, error) {
	*msgID++
	attrs := [][]byte{berOctetString([]byte("dn"))}
	if usernameAttr != "" {
		attrs = append(attrs, berOctetString([]byte(usernameAttr)))
	}

	req := berSequence(
		berInteger(*msgID),
		berConstructed(0x63, // SearchRequest [APPLICATION 3]
			berOctetString([]byte(baseDN)),     // baseObject
			berEnumerated(2),                   // scope: wholeSubtree
			berEnumerated(0),                   // derefAliases: neverDerefAliases
			berInteger(1),                      // sizeLimit
			berInteger(10),                     // timeLimit
			berBoolean(false),                  // typesOnly
			berParseFilter(filter),             // filter
			berSequence(attrs...),              // attributes
		),
	)
	if _, err := conn.Write(req); err != nil {
		return "", "", err
	}

	var userDN, foundUsername string
	for {
		resp, err := berReadMessage(conn)
		if err != nil {
			return "", "", err
		}
		tag, content := berUnwrapMessage(resp)

		if tag == 0x64 { // SearchResultEntry [APPLICATION 4]
			userDN, foundUsername = berExtractSearchEntry(content, usernameAttr)
		} else if tag == 0x65 { // SearchResultDone [APPLICATION 5]
			rc := berExtractResultCodeFromContent(content)
			if rc != 0 && rc != 4 { // 4 = sizeLimitExceeded (OK for sizeLimit=1)
				return "", "", fmt.Errorf("search failed: result code %d", rc)
			}
			break
		}
	}

	return userDN, foundUsername, nil
}

func ldapCheckGroupMembership(conn net.Conn, msgID *int, groupDN, memberAttr, userDN string) bool {
	*msgID++
	filter := fmt.Sprintf("(&(objectClass=*)(%s=%s))", ldapEscapeFilter(memberAttr), ldapEscapeFilter(userDN))
	req := berSequence(
		berInteger(*msgID),
		berConstructed(0x63, // SearchRequest
			berOctetString([]byte(groupDN)),    // baseObject = group DN
			berEnumerated(0),                   // scope: baseObject
			berEnumerated(0),                   // derefAliases
			berInteger(1),                      // sizeLimit
			berInteger(10),                     // timeLimit
			berBoolean(false),                  // typesOnly
			berParseFilter(filter),             // filter
			berSequence(berOctetString([]byte("dn"))),
		),
	)
	if _, err := conn.Write(req); err != nil {
		return false
	}

	found := false
	for {
		resp, err := berReadMessage(conn)
		if err != nil {
			return false
		}
		tag, _ := berUnwrapMessage(resp)
		if tag == 0x64 { // SearchResultEntry
			found = true
		} else if tag == 0x65 { // SearchResultDone
			break
		}
	}
	return found
}

// --- BER encoding helpers ---

func berInteger(val int) []byte {
	if val == 0 {
		return []byte{0x02, 1, 0}
	}
	var buf []byte
	v := val
	for v > 0 || (len(buf) > 0 && buf[0]&0x80 != 0) {
		buf = append([]byte{byte(v & 0xFF)}, buf...)
		v >>= 8
		if v == 0 && buf[0]&0x80 != 0 {
			buf = append([]byte{0}, buf...)
			break
		}
	}
	if len(buf) == 0 {
		buf = []byte{0}
	}
	return append([]byte{0x02, byte(len(buf))}, buf...)
}

func berEnumerated(val int) []byte {
	b := berInteger(val)
	b[0] = 0x0A // ENUMERATED tag
	return b
}

func berBoolean(val bool) []byte {
	v := byte(0x00)
	if val {
		v = 0xFF
	}
	return []byte{0x01, 1, v}
}

func berOctetString(data []byte) []byte {
	return berTagLen(0x04, data)
}

func berOctetStringWithTag(tag byte, data []byte) []byte {
	return berTagLen(tag, data)
}

func berSequence(items ...[]byte) []byte {
	var content []byte
	for _, item := range items {
		content = append(content, item...)
	}
	return berTagLen(0x30, content)
}

func berSet(items ...[]byte) []byte {
	var content []byte
	for _, item := range items {
		content = append(content, item...)
	}
	return berTagLen(0x31, content)
}

func berConstructed(tag byte, items ...[]byte) []byte {
	var content []byte
	for _, item := range items {
		content = append(content, item...)
	}
	return berTagLen(tag, content)
}

func berTagLen(tag byte, data []byte) []byte {
	l := len(data)
	var header []byte
	if l < 128 {
		header = []byte{tag, byte(l)}
	} else if l < 256 {
		header = []byte{tag, 0x81, byte(l)}
	} else if l < 65536 {
		header = []byte{tag, 0x82, byte(l >> 8), byte(l)}
	} else {
		b := make([]byte, 4)
		binary.BigEndian.PutUint32(b, uint32(l))
		// Trim leading zeros
		for len(b) > 1 && b[0] == 0 {
			b = b[1:]
		}
		header = append([]byte{tag, byte(0x80 | len(b))}, b...)
	}
	return append(header, data...)
}

// berParseFilter parses a simple LDAP filter string into BER encoding.
// Supports: &, |, !, =, =*, >=, <=, ~=, substring (with *)
func berParseFilter(filter string) []byte {
	filter = strings.TrimSpace(filter)
	if len(filter) < 2 || filter[0] != '(' || filter[len(filter)-1] != ')' {
		// Try wrapping
		filter = "(" + filter + ")"
	}
	return berParseFilterInner(filter)
}

func berParseFilterInner(f string) []byte {
	if len(f) < 2 {
		return berOctetString([]byte(f))
	}
	// Remove outer parens
	if f[0] == '(' && f[len(f)-1] == ')' {
		f = f[1 : len(f)-1]
	}

	switch f[0] {
	case '&':
		return berConstructed(0xA0, berParseFilterList(f[1:])...)
	case '|':
		return berConstructed(0xA1, berParseFilterList(f[1:])...)
	case '!':
		return berConstructed(0xA2, berParseFilterInner(f[1:]))
	default:
		return berParseFilterItem(f)
	}
}

func berParseFilterList(f string) [][]byte {
	var items [][]byte
	depth := 0
	start := -1
	for i, c := range f {
		if c == '(' {
			if depth == 0 {
				start = i
			}
			depth++
		} else if c == ')' {
			depth--
			if depth == 0 && start >= 0 {
				items = append(items, berParseFilterInner(f[start:i+1]))
				start = -1
			}
		}
	}
	return items
}

func berParseFilterItem(f string) []byte {
	// Handle presence test: attr=*
	if idx := strings.Index(f, "=*"); idx > 0 && idx == len(f)-2 {
		attr := f[:idx]
		return berConstructed(0x87, []byte(attr))
	}

	// Handle equality: attr=value
	if idx := strings.Index(f, "="); idx > 0 {
		attr := f[:idx]
		value := f[idx+1:]

		// Check for substring filter (contains *)
		if strings.Contains(value, "*") {
			return berSubstringFilter(attr, value)
		}

		// Simple equality match
		return berConstructed(0xA3, // equalityMatch
			berOctetString([]byte(attr)),
			berOctetString([]byte(value)),
		)
	}

	// Fallback: present filter
	return berConstructed(0x87, []byte(f))
}

func berSubstringFilter(attr, value string) []byte {
	parts := strings.Split(value, "*")
	var substrings [][]byte
	for i, part := range parts {
		if part == "" {
			continue
		}
		var tag byte
		if i == 0 {
			tag = 0x80 // initial
		} else if i == len(parts)-1 {
			tag = 0x82 // final
		} else {
			tag = 0x81 // any
		}
		substrings = append(substrings, berOctetStringWithTag(tag, []byte(part)))
	}
	return berConstructed(0xA4,
		berOctetString([]byte(attr)),
		berSequence(substrings...),
	)
}

// --- BER reading helpers ---

func berReadMessage(conn net.Conn) ([]byte, error) {
	conn.SetReadDeadline(time.Now().Add(15 * time.Second))
	// Read tag
	tagBuf := make([]byte, 1)
	if _, err := conn.Read(tagBuf); err != nil {
		return nil, err
	}
	// Read length
	lenBuf := make([]byte, 1)
	if _, err := conn.Read(lenBuf); err != nil {
		return nil, err
	}
	length := int(lenBuf[0])
	if lenBuf[0]&0x80 != 0 {
		numBytes := int(lenBuf[0] & 0x7F)
		lenBytes := make([]byte, numBytes)
		if _, err := readFull(conn, lenBytes); err != nil {
			return nil, err
		}
		length = 0
		for _, b := range lenBytes {
			length = (length << 8) | int(b)
		}
	}

	// Read content
	content := make([]byte, length)
	if _, err := readFull(conn, content); err != nil {
		return nil, err
	}

	// Reconstruct full message
	msg := append([]byte{tagBuf[0]}, berEncodeLength(length)...)
	msg = append(msg, content...)
	return msg, nil
}

func readFull(conn net.Conn, buf []byte) (int, error) {
	total := 0
	for total < len(buf) {
		n, err := conn.Read(buf[total:])
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

func berEncodeLength(l int) []byte {
	if l < 128 {
		return []byte{byte(l)}
	}
	if l < 256 {
		return []byte{0x81, byte(l)}
	}
	return []byte{0x82, byte(l >> 8), byte(l)}
}

func berUnwrapMessage(data []byte) (byte, []byte) {
	// SEQUENCE wrapper
	if len(data) < 2 || data[0] != 0x30 {
		return 0, nil
	}
	_, seqContent := berReadTagLen(data)

	// Skip messageID (INTEGER)
	_, rest := berSkipElement(seqContent)
	if len(rest) == 0 {
		return 0, nil
	}

	// Return the application tag and its content
	tag := rest[0]
	_, content := berReadTagLen(rest)
	return tag, content
}

func berReadTagLen(data []byte) (int, []byte) {
	if len(data) < 2 {
		return 0, nil
	}
	pos := 1
	length := int(data[pos])
	pos++
	if data[1]&0x80 != 0 {
		numBytes := int(data[1] & 0x7F)
		length = 0
		for i := 0; i < numBytes && pos < len(data); i++ {
			length = (length << 8) | int(data[pos])
			pos++
		}
	}
	if pos+length > len(data) {
		return length, data[pos:]
	}
	return length, data[pos : pos+length]
}

func berSkipElement(data []byte) ([]byte, []byte) {
	if len(data) < 2 {
		return nil, nil
	}
	pos := 1
	length := int(data[pos])
	pos++
	if data[1]&0x80 != 0 {
		numBytes := int(data[1] & 0x7F)
		length = 0
		for i := 0; i < numBytes && pos < len(data); i++ {
			length = (length << 8) | int(data[pos])
			pos++
		}
	}
	end := pos + length
	if end > len(data) {
		end = len(data)
	}
	return data[:end], data[end:]
}

func berExtractResultCode(msg []byte) int {
	_, content := berUnwrapMessage(msg)
	return berExtractResultCodeFromContent(content)
}

func berExtractResultCodeFromContent(content []byte) int {
	if len(content) < 3 {
		return -1
	}
	// First element should be ENUMERATED (result code)
	if content[0] == 0x0A && content[1] == 1 {
		return int(content[2])
	}
	return -1
}

func berExtractSearchEntry(content []byte, usernameAttr string) (string, string) {
	if len(content) < 2 {
		return "", ""
	}
	// First element: objectName (OCTET STRING)
	_, dnContent := berReadTagLen(content)
	dn := string(dnContent)

	// Skip past DN element
	_, rest := berSkipElement(content)

	// Second element: attributes (SEQUENCE of SEQUENCE)
	if len(rest) == 0 {
		return dn, ""
	}
	_, attrsContent := berReadTagLen(rest)

	var foundUsername string
	if usernameAttr != "" {
		foundUsername = berFindAttribute(attrsContent, usernameAttr)
	}

	return dn, foundUsername
}

func berFindAttribute(attrsData []byte, attrName string) string {
	data := attrsData
	for len(data) > 2 {
		elem, rest := berSkipElement(data)
		if len(elem) > 2 {
			_, seqContent := berReadTagLen(elem)
			if len(seqContent) > 2 {
				// First element: attribute type (OCTET STRING)
				_, typeContent := berReadTagLen(seqContent)
				if strings.EqualFold(string(typeContent), attrName) {
					// Second element: SET of values
					_, valRest := berSkipElement(seqContent)
					if len(valRest) > 2 {
						_, setContent := berReadTagLen(valRest)
						if len(setContent) > 2 {
							_, valContent := berReadTagLen(setContent)
							return string(valContent)
						}
					}
				}
			}
		}
		data = rest
	}
	return ""
}

func ldapEscapeFilter(s string) string {
	var b strings.Builder
	for _, c := range s {
		switch c {
		case '\\':
			b.WriteString("\\5c")
		case '*':
			b.WriteString("\\2a")
		case '(':
			b.WriteString("\\28")
		case ')':
			b.WriteString("\\29")
		case '\x00':
			b.WriteString("\\00")
		default:
			b.WriteRune(c)
		}
	}
	return b.String()
}
