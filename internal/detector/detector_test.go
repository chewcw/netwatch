package detector

import (
	"testing"
	"time"
)

func kinds(alerts []Alert) []Kind {
	out := make([]Kind, len(alerts))
	for i, a := range alerts {
		out[i] = a.Kind
	}
	return out
}

func TestSilentToAlertToRecover(t *testing.T) {
	d := New("c", 3, 0)
	// seed baseline
	if got := d.Feed(100, 100); len(got) != 0 {
		t.Fatalf("seed feed emitted alerts: %v", got)
	}
	// active
	if got := d.Feed(200, 200); len(got) != 0 {
		t.Fatalf("active feed emitted alerts: %v", got)
	}
	// two silent ticks: still normal
	if got := d.Feed(200, 200); len(got) != 0 {
		t.Fatalf("tick1 silent emitted: %v", got)
	}
	if got := d.Feed(200, 200); len(got) != 0 {
		t.Fatalf("tick2 silent emitted: %v", got)
	}
	// third silent tick: Alerted with sensor-side verdict (rx silent, tx silent)
	got := d.Feed(200, 200)
	if len(got) != 1 || got[0].Kind != KindAlerted {
		t.Fatalf("want 1 Alerted, got %v", kinds(got))
	}
	if !got[0].SilentRx || !got[0].SilentTx {
		t.Errorf("verdict = rx:%v tx:%v, want both silent", got[0].SilentRx, got[0].SilentTx)
	}
	if got[0].RxDelta != 0 || got[0].TxDelta != 0 {
		t.Errorf("deltas = %d/%d, want 0/0", got[0].RxDelta, got[0].TxDelta)
	}
	if d.State() != StateAlerting {
		t.Errorf("state = %v, want StateAlerting", d.State())
	}
	// stays alerting while silent: no more alerts
	if got := d.Feed(200, 200); len(got) != 0 {
		t.Fatalf("still silent emitted: %v", kinds(got))
	}
	// one active axis alone is not enough to recover (tx still silent)
	if got := d.Feed(300, 200); len(got) != 0 {
		t.Fatalf("rx-only recovery emitted: %v", kinds(got))
	}
	// both active: Recovered, back to normal
	got = d.Feed(400, 300)
	if len(got) != 1 || got[0].Kind != KindRecovered {
		t.Fatalf("want 1 Recovered, got %v", kinds(got))
	}
	if d.State() != StateNormal {
		t.Errorf("state = %v, want StateNormal", d.State())
	}
	// new incident fires a fresh Alerted (one per incident)
	if got := d.Feed(400, 300); len(got) != 0 {
		t.Fatalf("first silent of new incident emitted: %v", kinds(got))
	}
	if got := d.Feed(400, 300); len(got) != 0 {
		t.Fatalf("second silent emitted: %v", kinds(got))
	}
	got = d.Feed(400, 300)
	if len(got) != 1 || got[0].Kind != KindAlerted {
		t.Fatalf("want fresh Alerted, got %v", kinds(got))
	}
}

func TestCloudSideVerdict(t *testing.T) {
	// rx stays active, tx silent -> cloud-path suspect
	d := New("c", 2, 0)
	d.Feed(0, 0)          // seed
	d.Feed(100, 0)        // rx active, tx silent (tx silent tick 1)
	got := d.Feed(200, 0) // rx active, tx silent (tx silent tick 2) -> Alerted
	if len(got) != 1 || got[0].Kind != KindAlerted {
		t.Fatalf("want Alerted, got %v", kinds(got))
	}
	if got[0].SilentRx || !got[0].SilentTx {
		t.Errorf("verdict = rx:%v tx:%v, want rx active tx silent", got[0].SilentRx, got[0].SilentTx)
	}
}

func TestMinTrafficBoundary(t *testing.T) {
	// minTraffic=200: delta 200 is silent, delta 201 is active
	d := New("c", 3, 200)
	d.Feed(0, 0)
	d.Feed(200, 200) // silent (== threshold)
	d.Feed(200, 200) // silent again -> alert
	if got := d.Feed(200, 200); len(got) != 1 || got[0].Kind != KindAlerted {
		t.Fatalf("want Alerted on delta==minTraffic, got %v", kinds(got))
	}
	d2 := New("c", 3, 200)
	d2.Feed(0, 0)
	d2.Feed(201, 201) // active (> threshold)
	d2.Feed(201, 201) // still active, no alert
	if got := d2.Feed(201, 201); len(got) != 0 {
		t.Fatalf("delta>minTraffic emitted: %v", kinds(got))
	}
}

func TestCounterResetCountsSilent(t *testing.T) {
	// restart resets counters: rx goes 100 -> 5 (delta would be negative)
	d := New("c", 3, 0)
	d.Feed(100, 100)    // seed
	d.Feed(5, 5)        // counter reset -> silent tick 1
	d.Feed(5, 5)        // silent tick 2
	got := d.Feed(5, 5) // silent tick 3 -> alert
	if len(got) != 1 || got[0].Kind != KindAlerted {
		t.Fatalf("want Alerted after restart-reset ticks, got %v", kinds(got))
	}
}

func TestDeadAndBack(t *testing.T) {
	d := New("c", 2, 0)
	d.Feed(100, 100)
	// brief 404 (restart) must not fire Dead
	if got := d.FeedDead(); len(got) != 0 {
		t.Fatalf("first 404 emitted: %v", kinds(got))
	}
	d.Feed(110, 110) // back, still normal
	if got := d.FeedDead(); len(got) != 0 {
		t.Fatalf("brief 404 emitted: %v", kinds(got))
	}
	if got := d.FeedDead(); len(got) != 1 || got[0].Kind != KindDead {
		t.Fatalf("want Dead after persistent 404, got %v", kinds(got))
	}
	if d.State() != StateDead {
		t.Errorf("state = %v, want StateDead", d.State())
	}
	if got := d.Feed(120, 120); len(got) != 1 || got[0].Kind != KindBack {
		t.Fatalf("want Back on container return, got %v", kinds(got))
	}
	if d.State() != StateNormal {
		t.Errorf("state = %v, want StateNormal", d.State())
	}
	if d.DeadTicks() != 0 || d.SilentRxTicks() != 0 || d.SilentTxTicks() != 0 {
		t.Errorf("counters not reset after Back: %+v", d)
	}
}

func TestThresholdMinimumOne(t *testing.T) {
	d := New("c", 1, 0)
	d.Feed(100, 100)
	got := d.Feed(100, 100) // first silent tick already meets threshold 1
	if len(got) != 1 || got[0].Kind != KindAlerted {
		t.Fatalf("want Alerted at threshold 1, got %v", kinds(got))
	}
}

func TestAlertTimestampSet(t *testing.T) {
	d := New("c", 1, 0)
	d.Feed(1, 1)
	before := time.Now()
	got := d.Feed(1, 1)
	if len(got) != 1 || got[0].At.Before(before) {
		t.Fatalf("alert timestamp missing or wrong: %+v", got)
	}
}
