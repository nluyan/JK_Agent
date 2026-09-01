package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/gosnmp/gosnmp"
)

func TestIsSNMPCommand(t *testing.T) {
	if !isSNMPCommand(" \r\n" + SNMPCommandPrefix + ` {"operation":"get"}`) {
		t.Fatal("SNMP command should allow leading whitespace")
	}
	if isSNMPCommand("Write-Output '" + SNMPCommandPrefix + "'") {
		t.Fatal("SNMP prefix in a normal script must not be treated as a command")
	}
}

func TestParseSNMPCommandDefaults(t *testing.T) {
	command, err := parseSNMPCommand(SNMPCommandPrefix + `{
		"operation":"GET",
		"target":"192.0.2.10",
		"community":"public",
		"oids":["1.3.6.1.2.1.1.1.0"]
	}`)
	if err != nil {
		t.Fatalf("parse command: %v", err)
	}
	if command.Operation != "get" || command.Port != 161 || command.Transport != "udp" {
		t.Fatalf("unexpected normalized command: %+v", command)
	}
	if command.TimeoutSeconds != defaultSNMPRequestTimeout || command.OperationTimeoutSeconds != defaultSNMPOperationLimit {
		t.Fatalf("unexpected timeout defaults: %+v", command)
	}
	if command.MaxRepetitions != 25 || command.MaxResults != defaultSNMPMaxResults {
		t.Fatalf("unexpected result defaults: %+v", command)
	}
}

func TestParseSNMPCommandRejectsUnknownField(t *testing.T) {
	_, err := parseSNMPCommand(SNMPCommandPrefix + `{"operation":"get","target":"127.0.0.1","community":"public","unknown":true}`)
	if err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("expected unknown field error, got %v", err)
	}
}

func TestParseSNMPCommandRejectsNULTarget(t *testing.T) {
	_, err := parseSNMPCommand(SNMPCommandPrefix + "{\"operation\":\"get\",\"target\":\"bad\\u0000target\",\"community\":\"public\",\"oid\":\"1.3.6.1\"}")
	if err == nil || !strings.Contains(err.Error(), "NUL") {
		t.Fatalf("expected NUL target error, got %v", err)
	}
}

func TestNewSNMPClientV3AuthPriv(t *testing.T) {
	command := snmpCommand{
		Target:                  "192.0.2.20",
		Port:                    161,
		Transport:               "udp",
		Version:                 "3",
		Username:                "snmp-user",
		SecurityLevel:           "authPriv",
		AuthProtocol:            "sha256",
		AuthPassphrase:          "auth-secret",
		PrivProtocol:            "aes128",
		PrivPassphrase:          "priv-secret",
		TimeoutSeconds:          10,
		OperationTimeoutSeconds: 60,
	}
	client, err := newSNMPClient(command, context.Background())
	if err != nil {
		t.Fatalf("create v3 client: %v", err)
	}
	if client.Version != gosnmp.Version3 || client.MsgFlags != gosnmp.AuthPriv {
		t.Fatalf("unexpected v3 client settings: version=%v flags=%v", client.Version, client.MsgFlags)
	}
	security, ok := client.SecurityParameters.(*gosnmp.UsmSecurityParameters)
	if !ok {
		t.Fatalf("unexpected security parameters type %T", client.SecurityParameters)
	}
	if security.AuthenticationProtocol != gosnmp.SHA256 || security.PrivacyProtocol != gosnmp.AES {
		t.Fatalf("unexpected v3 protocols: auth=%v priv=%v", security.AuthenticationProtocol, security.PrivacyProtocol)
	}
}

func TestBuildSNMPSetPDU(t *testing.T) {
	command := snmpCommand{
		Operation: "set",
		OID:       "1.3.6.1.2.1.1.5.0",
		ValueType: "string",
		Value:     json.RawMessage(`"agent-name"`),
	}
	pdu, err := buildSNMPSetPDU(command)
	if err != nil {
		t.Fatalf("build set PDU: %v", err)
	}
	if pdu.Name != ".1.3.6.1.2.1.1.5.0" || pdu.Type != gosnmp.OctetString || pdu.Value != "agent-name" {
		t.Fatalf("unexpected PDU: %+v", pdu)
	}
}

func TestBuildSNMPIntegerSetPDUUsesGosnmpCompatibleType(t *testing.T) {
	command := snmpCommand{
		Operation: "set",
		OID:       "1.3.6.1.2.1.1.7.0",
		ValueType: "integer",
		Value:     json.RawMessage(`72`),
	}
	pdu, err := buildSNMPSetPDU(command)
	if err != nil {
		t.Fatalf("build integer set PDU: %v", err)
	}
	if _, ok := pdu.Value.(int); !ok {
		t.Fatalf("gosnmp v1.38 requires Integer SET values to use int, got %T", pdu.Value)
	}
	packet := &gosnmp.SnmpPacket{
		Version:   gosnmp.Version2c,
		Community: "private",
		PDUType:   gosnmp.SetRequest,
		RequestID: 1,
		Variables: []gosnmp.SnmpPDU{pdu},
	}
	if _, err := packet.MarshalMsg(); err != nil {
		t.Fatalf("marshal integer SET packet: %v", err)
	}
}

func TestFormatSNMPVariablesPreservesOctets(t *testing.T) {
	variables := formatSNMPVariables([]gosnmp.SnmpPDU{{
		Name:  ".1.3.6.1",
		Type:  gosnmp.OctetString,
		Value: []byte{0xff, 0x00, 0x01},
	}})
	if len(variables) != 1 || variables[0].Value != nil || variables[0].ValueBase64 != "/wAB" {
		t.Fatalf("unexpected formatted variable: %+v", variables)
	}
}

func TestExecuteSNMPCommandGetAgainstLocalAgent(t *testing.T) {
	listener, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatalf("listen UDP: %v", err)
	}
	defer listener.Close()
	if err := listener.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}

	serverResult := make(chan error, 1)
	go func() {
		buffer := make([]byte, 65535)
		n, remote, err := listener.ReadFromUDP(buffer)
		if err != nil {
			serverResult <- err
			return
		}
		logger := gosnmp.NewLogger(log.New(io.Discard, "", 0))
		decoder := &gosnmp.GoSNMP{Logger: logger}
		request, err := decoder.SnmpDecodePacket(buffer[:n])
		if err != nil {
			serverResult <- fmt.Errorf("decode request: %w", err)
			return
		}
		response := &gosnmp.SnmpPacket{
			Version:   request.Version,
			Community: request.Community,
			PDUType:   gosnmp.GetResponse,
			RequestID: request.RequestID,
			Variables: []gosnmp.SnmpPDU{{
				Name:  request.Variables[0].Name,
				Type:  gosnmp.OctetString,
				Value: []byte("test-device"),
			}},
			Logger: logger,
		}
		data, err := response.MarshalMsg()
		if err != nil {
			serverResult <- fmt.Errorf("marshal response: %w", err)
			return
		}
		_, err = listener.WriteToUDP(data, remote)
		serverResult <- err
	}()

	port := listener.LocalAddr().(*net.UDPAddr).Port
	script := fmt.Sprintf(`%s{"operation":"get","target":"127.0.0.1","port":%d,"version":"2c","community":"public","timeoutSeconds":2,"operationTimeoutSeconds":5,"oids":["1.3.6.1.2.1.1.5.0"]}`, SNMPCommandPrefix, port)
	text := executeSNMPCommand(script)
	if err := <-serverResult; err != nil {
		t.Fatalf("local SNMP agent: %v", err)
	}

	var result snmpResult
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		t.Fatalf("result is not JSON: %v (%s)", err, text)
	}
	if !result.Success || len(result.Variables) != 1 || result.Variables[0].Value != "test-device" {
		t.Fatalf("unexpected SNMP result: %+v", result)
	}
}

func TestExecuteSNMPCommandReturnsJSONError(t *testing.T) {
	text := executeSNMPCommand(SNMPCommandPrefix + `{"operation":"invalid","target":"127.0.0.1"}`)
	var result snmpResult
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		t.Fatalf("result is not JSON: %v (%s)", err, text)
	}
	if result.Success || result.Error == "" {
		t.Fatalf("expected structured error result, got %+v", result)
	}
}
