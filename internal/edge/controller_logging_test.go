// SPDX-License-Identifier: MIT

package edge

import "testing"

func TestEdgeAuditControllerAddressRedactsCredentials(t *testing.T) {
	t.Parallel()
	c, logger, _ := newAuditedTestClient(t)
	raw := "https://hidden-user:hidden-password@controller.example/base?credential=query-secret#fragment-secret"
	c.cfg.DrydockURL = raw
	isExec, admitted := c.admitRawExecStart("POST", "/exec/redaction/start")
	if !isExec || !admitted {
		t.Fatal("test exec was not admitted")
	}
	defer c.releaseRawExecSlot()
	records := logger.Records(0)
	if len(records) != 1 || records[0].Actor != "https://controller.example/base" {
		t.Fatalf("audit actor was not redacted: %+v", records)
	}
	if c.cfg.DrydockURL != raw {
		t.Fatal("audit redaction changed the connection URL")
	}
}
