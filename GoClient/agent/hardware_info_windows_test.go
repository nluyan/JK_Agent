//go:build windows

package agent

import (
	"encoding/json"
	"testing"
)

func TestPrinterInfoIncludesSharedUSBPrinters(t *testing.T) {
	// Reproduce the reported WMI inventory: all three USB queues are shared.
	rows := []printerWMI{
		{Name: "Gprinter GP-3120TUB", PortName: "USB002", Shared: true},
		{Name: "Canon G2010 series (副本 1)", PortName: "USB002", Shared: true},
		{Name: "Canon G2010 series", PortName: "USB001", Shared: true},
		{Name: "Microsoft Print to PDF", PortName: "PORTPROMPT:"},
		{Name: "Remote printer", Network: true},
		{Name: "Shared remote printer", Network: true, Shared: true},
	}
	data, err := json.Marshal(printerInfoFromRows(rows))
	if err != nil {
		t.Fatal(err)
	}
	var printers []struct {
		Name    string `json:"名称"`
		Port    string `json:"端口"`
		Shared  bool   `json:"共享打印机"`
		Network bool   `json:"网络打印机"`
	}
	if err := json.Unmarshal(data, &printers); err != nil {
		t.Fatal(err)
	}
	if len(printers) != 4 {
		t.Fatalf("expected 3 shared USB printers and 1 local virtual printer, got %s", data)
	}
	for i, printer := range printers {
		if printer.Name != rows[i].Name || printer.Port != rows[i].PortName || printer.Shared != rows[i].Shared || printer.Network {
			t.Errorf("printer %d lost inventory fields: %+v", i, printer)
		}
	}
}

func TestPrinterInfoNoLocalPrinters(t *testing.T) {
	for _, rows := range [][]printerWMI{nil, {{Name: "Remote printer", Network: true}}} {
		if got := printerInfoFromRows(rows); got != nil {
			t.Errorf("expected nil for no local printers, got %#v", got)
		}
	}
}
