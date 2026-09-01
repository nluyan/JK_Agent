package agent

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gosnmp/gosnmp"
)

// SNMPCommandPrefix marks a JSON command sent through ExecutePowershellScript.
// The command is handled natively by the Go client and never starts PowerShell.
const SNMPCommandPrefix = "__JK_AGENT_SNMP__"

const (
	defaultSNMPRequestTimeout = 10
	defaultSNMPOperationLimit = 240
	defaultSNMPMaxResults     = 10000
	maxSNMPCommandBytes       = 1024 * 1024
	maxSNMPResultBytes        = 8 * 1024 * 1024
)

var errSNMPResultLimit = errors.New("SNMP结果数量超过限制")

type snmpCommand struct {
	Operation               string          `json:"operation"`
	Target                  string          `json:"target"`
	Port                    uint16          `json:"port,omitempty"`
	Transport               string          `json:"transport,omitempty"`
	Version                 string          `json:"version,omitempty"`
	Community               string          `json:"community,omitempty"`
	Username                string          `json:"username,omitempty"`
	AuthProtocol            string          `json:"authProtocol,omitempty"`
	AuthPassphrase          string          `json:"authPassphrase,omitempty"`
	PrivProtocol            string          `json:"privProtocol,omitempty"`
	PrivPassphrase          string          `json:"privPassphrase,omitempty"`
	SecurityLevel           string          `json:"securityLevel,omitempty"`
	ContextName             string          `json:"contextName,omitempty"`
	ContextEngineID         string          `json:"contextEngineId,omitempty"`
	TimeoutSeconds          int             `json:"timeoutSeconds,omitempty"`
	OperationTimeoutSeconds int             `json:"operationTimeoutSeconds,omitempty"`
	Retries                 int             `json:"retries,omitempty"`
	MaxRepetitions          uint32          `json:"maxRepetitions,omitempty"`
	MaxResults              int             `json:"maxResults,omitempty"`
	NonRepeaters            uint8           `json:"nonRepeaters,omitempty"`
	OIDs                    []string        `json:"oids,omitempty"`
	OID                     string          `json:"oid,omitempty"`
	Value                   json.RawMessage `json:"value,omitempty"`
	ValueType               string          `json:"valueType,omitempty"`
}

type snmpResult struct {
	Success   bool           `json:"success"`
	Operation string         `json:"operation,omitempty"`
	Target    string         `json:"target,omitempty"`
	Variables []snmpVariable `json:"variables,omitempty"`
	Error     string         `json:"error,omitempty"`
}

type snmpVariable struct {
	OID         string `json:"oid"`
	Type        string `json:"type"`
	Value       any    `json:"value,omitempty"`
	ValueBase64 string `json:"valueBase64,omitempty"`
}

func isSNMPCommand(script string) bool {
	return strings.HasPrefix(strings.TrimSpace(script), SNMPCommandPrefix)
}

func executeSNMPCommand(script string) (output string) {
	result := snmpResult{}
	defer func() {
		if recovered := recover(); recovered != nil {
			result.Success = false
			result.Variables = nil
			result.Error = fmt.Sprintf("执行SNMP操作时发生内部错误: %v", recovered)
			output = marshalSNMPResult(result)
		}
	}()
	command, err := parseSNMPCommand(script)
	if err != nil {
		result.Error = err.Error()
		return marshalSNMPResult(result)
	}
	result.Operation = command.Operation
	result.Target = command.Target

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(command.OperationTimeoutSeconds)*time.Second)
	defer cancel()

	client, err := newSNMPClient(command, ctx)
	if err != nil {
		result.Error = err.Error()
		return marshalSNMPResult(result)
	}
	if err := client.Connect(); err != nil {
		result.Error = fmt.Sprintf("SNMP连接失败: %v", err)
		return marshalSNMPResult(result)
	}
	defer client.Conn.Close()

	variables, err := runSNMPOperation(client, command)
	result.Variables = formatSNMPVariables(variables)
	if err != nil {
		result.Error = err.Error()
		return marshalSNMPResult(result)
	}

	result.Success = true
	return marshalSNMPResult(result)
}

func parseSNMPCommand(script string) (snmpCommand, error) {
	var command snmpCommand
	payload := strings.TrimSpace(script)
	if !strings.HasPrefix(payload, SNMPCommandPrefix) {
		return command, fmt.Errorf("不是有效的SNMP特殊命令")
	}
	payload = strings.TrimSpace(strings.TrimPrefix(payload, SNMPCommandPrefix))
	if payload == "" {
		return command, fmt.Errorf("SNMP命令参数不能为空")
	}
	if len(payload) > maxSNMPCommandBytes {
		return command, fmt.Errorf("SNMP命令不能超过%d字节", maxSNMPCommandBytes)
	}

	decoder := json.NewDecoder(strings.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&command); err != nil {
		return command, fmt.Errorf("解析SNMP命令失败: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return command, fmt.Errorf("SNMP命令只能包含一个JSON对象")
		}
		return command, fmt.Errorf("SNMP命令包含无效的尾部内容: %w", err)
	}

	command.Operation = strings.ToLower(strings.TrimSpace(command.Operation))
	command.Target = strings.TrimSpace(command.Target)
	command.Transport = strings.ToLower(strings.TrimSpace(command.Transport))
	if command.Operation == "" {
		return command, fmt.Errorf("SNMP操作不能为空")
	}
	switch command.Operation {
	case "get", "getnext", "getbulk", "walk", "set":
	default:
		return command, fmt.Errorf("不支持的SNMP操作 %q，可选值: get、getnext、getbulk、walk、set", command.Operation)
	}
	if command.Target == "" {
		return command, fmt.Errorf("SNMP目标地址不能为空")
	}
	if strings.ContainsRune(command.Target, '\x00') {
		return command, fmt.Errorf("SNMP目标地址不能包含NUL字符")
	}
	if command.Port == 0 {
		command.Port = 161
	}
	if command.Transport == "" {
		command.Transport = "udp"
	}
	if command.Transport != "udp" && command.Transport != "tcp" {
		return command, fmt.Errorf("transport只能是udp或tcp")
	}
	if command.TimeoutSeconds == 0 {
		command.TimeoutSeconds = defaultSNMPRequestTimeout
	}
	if command.TimeoutSeconds < 1 || command.TimeoutSeconds > 60 {
		return command, fmt.Errorf("timeoutSeconds必须在1到60之间")
	}
	if command.OperationTimeoutSeconds == 0 {
		command.OperationTimeoutSeconds = defaultSNMPOperationLimit
	}
	if command.OperationTimeoutSeconds < 1 || command.OperationTimeoutSeconds > defaultSNMPOperationLimit {
		return command, fmt.Errorf("operationTimeoutSeconds必须在1到%d之间", defaultSNMPOperationLimit)
	}
	if command.Retries < 0 || command.Retries > 5 {
		return command, fmt.Errorf("retries必须在0到5之间")
	}
	if command.MaxRepetitions == 0 {
		command.MaxRepetitions = 25
	}
	if command.MaxRepetitions > 1000 {
		return command, fmt.Errorf("maxRepetitions不能大于1000")
	}
	if command.MaxResults == 0 {
		command.MaxResults = defaultSNMPMaxResults
	}
	if command.MaxResults < 1 || command.MaxResults > 50000 {
		return command, fmt.Errorf("maxResults必须在1到50000之间")
	}
	return command, nil
}

func newSNMPClient(command snmpCommand, ctx context.Context) (*gosnmp.GoSNMP, error) {
	version, err := parseSNMPVersion(command.Version)
	if err != nil {
		return nil, err
	}
	if version != gosnmp.Version3 && command.Community == "" {
		return nil, fmt.Errorf("SNMPv1/v2c community不能为空")
	}

	client := &gosnmp.GoSNMP{
		Target:          command.Target,
		Port:            command.Port,
		Transport:       command.Transport,
		Version:         version,
		Community:       command.Community,
		Context:         ctx,
		Timeout:         time.Duration(command.TimeoutSeconds) * time.Second,
		Retries:         command.Retries,
		MaxRepetitions:  command.MaxRepetitions,
		ContextName:     command.ContextName,
		ContextEngineID: command.ContextEngineID,
	}
	if version == gosnmp.Version3 {
		security, err := newSNMPv3SecurityParameters(command)
		if err != nil {
			return nil, err
		}
		client.SecurityModel = gosnmp.UserSecurityModel
		client.MsgFlags = security.msgFlags
		client.SecurityParameters = security.parameters
	}
	return client, nil
}

type snmpv3SecurityParameters struct {
	parameters *gosnmp.UsmSecurityParameters
	msgFlags   gosnmp.SnmpV3MsgFlags
}

func newSNMPv3SecurityParameters(command snmpCommand) (*snmpv3SecurityParameters, error) {
	level := normalizeSNMPName(command.SecurityLevel)
	if level == "" {
		level = "noauthnopriv"
	}
	var msgFlags gosnmp.SnmpV3MsgFlags
	switch level {
	case "noauthnopriv":
		msgFlags = gosnmp.NoAuthNoPriv
	case "authnopriv":
		msgFlags = gosnmp.AuthNoPriv
	case "authpriv":
		msgFlags = gosnmp.AuthPriv
	default:
		return nil, fmt.Errorf("不支持的SNMPv3 securityLevel %q", command.SecurityLevel)
	}
	if command.Username == "" {
		return nil, fmt.Errorf("SNMPv3 username不能为空")
	}

	parameters := &gosnmp.UsmSecurityParameters{
		UserName:                 command.Username,
		AuthenticationProtocol:   gosnmp.NoAuth,
		PrivacyProtocol:          gosnmp.NoPriv,
		AuthenticationPassphrase: command.AuthPassphrase,
		PrivacyPassphrase:        command.PrivPassphrase,
	}
	if msgFlags == gosnmp.AuthNoPriv || msgFlags == gosnmp.AuthPriv {
		parameters.AuthenticationProtocol, _ = parseSNMPAuthProtocol(command.AuthProtocol)
		if parameters.AuthenticationProtocol == gosnmp.NoAuth {
			return nil, fmt.Errorf("不支持的SNMPv3 authProtocol %q", command.AuthProtocol)
		}
		if parameters.AuthenticationPassphrase == "" {
			return nil, fmt.Errorf("SNMPv3认证模式需要authPassphrase")
		}
	}
	if msgFlags == gosnmp.AuthPriv {
		parameters.PrivacyProtocol, _ = parseSNMPPrivProtocol(command.PrivProtocol)
		if parameters.PrivacyProtocol == gosnmp.NoPriv {
			return nil, fmt.Errorf("不支持的SNMPv3 privProtocol %q", command.PrivProtocol)
		}
		if parameters.PrivacyPassphrase == "" {
			return nil, fmt.Errorf("SNMPv3加密模式需要privPassphrase")
		}
	}
	return &snmpv3SecurityParameters{parameters: parameters, msgFlags: msgFlags}, nil
}

func parseSNMPVersion(version string) (gosnmp.SnmpVersion, error) {
	switch normalizeSNMPName(version) {
	case "", "2", "2c", "v2c", "version2c":
		return gosnmp.Version2c, nil
	case "1", "v1", "version1":
		return gosnmp.Version1, nil
	case "3", "v3", "version3":
		return gosnmp.Version3, nil
	default:
		return 0, fmt.Errorf("不支持的SNMP版本 %q，可选值: 1、2c、3", version)
	}
}

func parseSNMPAuthProtocol(protocol string) (gosnmp.SnmpV3AuthProtocol, error) {
	switch normalizeSNMPName(protocol) {
	case "md5":
		return gosnmp.MD5, nil
	case "sha", "sha1":
		return gosnmp.SHA, nil
	case "sha224":
		return gosnmp.SHA224, nil
	case "sha256":
		return gosnmp.SHA256, nil
	case "sha384":
		return gosnmp.SHA384, nil
	case "sha512":
		return gosnmp.SHA512, nil
	default:
		return gosnmp.NoAuth, fmt.Errorf("不支持的SNMPv3 authProtocol %q", protocol)
	}
}

func parseSNMPPrivProtocol(protocol string) (gosnmp.SnmpV3PrivProtocol, error) {
	switch normalizeSNMPName(protocol) {
	case "des":
		return gosnmp.DES, nil
	case "aes", "aes128":
		return gosnmp.AES, nil
	case "aes192":
		return gosnmp.AES192, nil
	case "aes256":
		return gosnmp.AES256, nil
	case "aes192c":
		return gosnmp.AES192C, nil
	case "aes256c":
		return gosnmp.AES256C, nil
	default:
		return gosnmp.NoPriv, fmt.Errorf("不支持的SNMPv3 privProtocol %q", protocol)
	}
}

func normalizeSNMPName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "-", "")
	return strings.ReplaceAll(value, "_", "")
}

func runSNMPOperation(client *gosnmp.GoSNMP, command snmpCommand) ([]gosnmp.SnmpPDU, error) {
	switch command.Operation {
	case "get":
		oids, err := commandOIDs(command)
		if err != nil {
			return nil, err
		}
		packet, err := client.Get(oids)
		return snmpPacketVariables(packet, err)
	case "getnext":
		oids, err := commandOIDs(command)
		if err != nil {
			return nil, err
		}
		packet, err := client.GetNext(oids)
		return snmpPacketVariables(packet, err)
	case "getbulk":
		oids, err := commandOIDs(command)
		if err != nil {
			return nil, err
		}
		if client.Version == gosnmp.Version1 {
			return nil, fmt.Errorf("SNMPv1不支持getbulk操作")
		}
		packet, err := client.GetBulk(oids, command.NonRepeaters, command.MaxRepetitions)
		return snmpPacketVariables(packet, err)
	case "walk":
		return walkSNMPVariables(client, command)
	case "set":
		pdu, err := buildSNMPSetPDU(command)
		if err != nil {
			return nil, err
		}
		packet, err := client.Set([]gosnmp.SnmpPDU{pdu})
		return snmpPacketVariables(packet, err)
	default:
		return nil, fmt.Errorf("不支持的SNMP操作 %q", command.Operation)
	}
}

func commandOIDs(command snmpCommand) ([]string, error) {
	oids := append([]string(nil), command.OIDs...)
	if len(oids) == 0 && command.OID != "" {
		oids = []string{command.OID}
	}
	if len(oids) == 0 {
		return nil, fmt.Errorf("操作需要至少一个oid")
	}
	if len(oids) > gosnmp.MaxOids {
		return nil, fmt.Errorf("单次操作的oid数量不能超过%d", gosnmp.MaxOids)
	}
	for i := range oids {
		oids[i] = strings.TrimSpace(oids[i])
		if oids[i] == "" {
			return nil, fmt.Errorf("oid不能包含空值")
		}
		oids[i] = normalizeOID(oids[i])
	}
	return oids, nil
}

func walkSNMPVariables(client *gosnmp.GoSNMP, command snmpCommand) ([]gosnmp.SnmpPDU, error) {
	oids, err := commandOIDs(command)
	if err != nil {
		return nil, err
	}
	if len(oids) != 1 {
		return nil, fmt.Errorf("walk操作只支持一个根oid")
	}

	variables := make([]gosnmp.SnmpPDU, 0)
	walkFn := func(variable gosnmp.SnmpPDU) error {
		if len(variables) >= command.MaxResults {
			return errSNMPResultLimit
		}
		variables = append(variables, variable)
		return nil
	}
	if client.Version == gosnmp.Version1 {
		err = client.Walk(oids[0], walkFn)
	} else {
		err = client.BulkWalk(oids[0], walkFn)
	}
	if errors.Is(err, errSNMPResultLimit) {
		return variables, fmt.Errorf("%w，maxResults=%d", errSNMPResultLimit, command.MaxResults)
	}
	return variables, err
}

func buildSNMPSetPDU(command snmpCommand) (gosnmp.SnmpPDU, error) {
	oids, err := commandOIDs(command)
	if err != nil {
		return gosnmp.SnmpPDU{}, err
	}
	if len(oids) != 1 {
		return gosnmp.SnmpPDU{}, fmt.Errorf("set操作只支持一个oid")
	}
	if len(command.Value) == 0 {
		return gosnmp.SnmpPDU{}, fmt.Errorf("set操作需要value")
	}

	valueType := normalizeSNMPName(command.ValueType)
	if valueType == "" {
		return gosnmp.SnmpPDU{}, fmt.Errorf("set操作需要valueType")
	}

	var value any
	var pduType gosnmp.Asn1BER
	switch valueType {
	case "integer", "int":
		pduType = gosnmp.Integer
		value, err = parseInt32Value(command.Value, "integer")
	case "string", "octetstring", "octets":
		pduType = gosnmp.OctetString
		err = json.Unmarshal(command.Value, &value)
		if err == nil {
			if _, ok := value.(string); !ok {
				err = fmt.Errorf("string value必须是字符串")
			}
		}
	case "base64", "octetstringbase64":
		pduType = gosnmp.OctetString
		var encoded string
		if err = json.Unmarshal(command.Value, &encoded); err == nil {
			value, err = base64.StdEncoding.DecodeString(encoded)
		}
	case "oid", "objectidentifier":
		pduType = gosnmp.ObjectIdentifier
		var oid string
		if err = json.Unmarshal(command.Value, &oid); err == nil {
			if strings.TrimSpace(oid) == "" {
				err = fmt.Errorf("oid value不能为空")
			} else {
				value = normalizeOID(strings.TrimSpace(oid))
			}
		}
	case "gauge32", "gauge":
		pduType = gosnmp.Gauge32
		value, err = parseUint32Value(command.Value, "gauge32")
	case "counter32", "counter":
		pduType = gosnmp.Counter32
		value, err = parseUint32Value(command.Value, "counter32")
	case "counter64":
		pduType = gosnmp.Counter64
		value, err = parseUint64Value(command.Value, "counter64")
	case "timeticks", "timetick":
		pduType = gosnmp.TimeTicks
		value, err = parseUint32Value(command.Value, "timeticks")
	case "ipaddress", "ip":
		pduType = gosnmp.IPAddress
		var ip string
		if err = json.Unmarshal(command.Value, &ip); err == nil {
			if net.ParseIP(ip) == nil {
				err = fmt.Errorf("ipaddress value必须是有效IP地址")
			} else {
				value = ip
			}
		}
	default:
		return gosnmp.SnmpPDU{}, fmt.Errorf("不支持的valueType %q", command.ValueType)
	}
	if err != nil {
		return gosnmp.SnmpPDU{}, fmt.Errorf("解析%s value失败: %w", valueType, err)
	}
	return gosnmp.SnmpPDU{Name: oids[0], Type: pduType, Value: value}, nil
}

func parseInt32Value(raw json.RawMessage, name string) (int, error) {
	number, err := parseJSONNumber(raw, name)
	if err != nil {
		return 0, err
	}
	value, err := strconv.ParseInt(number.String(), 10, 32)
	if err != nil {
		return 0, fmt.Errorf("%s value必须是32位整数", name)
	}
	return int(value), nil
}

func parseUint32Value(raw json.RawMessage, name string) (uint32, error) {
	number, err := parseJSONNumber(raw, name)
	if err != nil {
		return 0, err
	}
	value, err := strconv.ParseUint(number.String(), 10, 32)
	if err != nil {
		return 0, fmt.Errorf("%s value必须是32位无符号整数", name)
	}
	return uint32(value), nil
}

func parseUint64Value(raw json.RawMessage, name string) (uint64, error) {
	number, err := parseJSONNumber(raw, name)
	if err != nil {
		return 0, err
	}
	value, err := strconv.ParseUint(number.String(), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s value必须是64位无符号整数", name)
	}
	return value, nil
}

func parseJSONNumber(raw json.RawMessage, name string) (json.Number, error) {
	var number json.Number
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&number); err != nil {
		return number, fmt.Errorf("解析%s value失败: %w", name, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return number, fmt.Errorf("%s value包含多余内容", name)
	}
	return number, nil
}

func normalizeOID(oid string) string {
	if strings.HasPrefix(oid, ".") {
		return oid
	}
	return "." + oid
}

func snmpPacketVariables(packet *gosnmp.SnmpPacket, err error) ([]gosnmp.SnmpPDU, error) {
	if err != nil {
		return nil, err
	}
	if packet == nil {
		return nil, fmt.Errorf("SNMP响应为空")
	}
	if packet.Error != gosnmp.NoError {
		return packet.Variables, fmt.Errorf("SNMP错误: %s (index %d)", packet.Error.String(), packet.ErrorIndex)
	}
	return packet.Variables, nil
}

func formatSNMPVariables(variables []gosnmp.SnmpPDU) []snmpVariable {
	if len(variables) == 0 {
		return nil
	}
	results := make([]snmpVariable, 0, len(variables))
	for _, variable := range variables {
		result := snmpVariable{OID: variable.Name, Type: variable.Type.String()}
		switch value := variable.Value.(type) {
		case []byte:
			result.ValueBase64 = base64.StdEncoding.EncodeToString(value)
			if utf8.Valid(value) {
				result.Value = string(value)
			}
		default:
			result.Value = value
		}
		results = append(results, result)
	}
	return results
}

func marshalSNMPResult(result snmpResult) string {
	data, err := json.Marshal(result)
	if err == nil && len(data) > maxSNMPResultBytes {
		result.Success = false
		result.Variables = nil
		result.Error = fmt.Sprintf("SNMP结果超过%d字节限制，请缩小OID范围或maxResults", maxSNMPResultBytes)
		data, err = json.Marshal(result)
	}
	if err != nil {
		fallback := snmpResult{
			Operation: result.Operation,
			Target:    result.Target,
			Error:     fmt.Sprintf("序列化SNMP结果失败: %v", err),
		}
		data, _ = json.Marshal(fallback)
	}
	return string(data)
}
