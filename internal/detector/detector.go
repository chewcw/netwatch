package detector

import (
	"fmt"
	"time"
)

type Kind int

const (
	KindAlerted Kind = iota
	KindRecovered
	KindDead
	KindBack
)

func (k Kind) String() string {
	switch k {
	case KindAlerted:
		return "alerted"
	case KindRecovered:
		return "recovered"
	case KindDead:
		return "dead"
	case KindBack:
		return "back"
	}
	return "unknown"
}

type Alert struct {
	Target   string
	Kind     Kind
	RxDelta  uint64
	TxDelta  uint64
	SilentRx bool
	SilentTx bool
	At       time.Time
}

type State int

const (
	StateNormal State = iota
	StateAlerting
	StateDead
)

func (s State) String() string {
	switch s {
	case StateNormal:
		return "normal"
	case StateAlerting:
		return "alerting"
	case StateDead:
		return "dead"
	}
	return "unknown"
}

type Detector struct {
	target         string
	threshold      int
	minTraffic     uint64
	lastRx, lastTx uint64
	haveBaseline   bool

	silentRxTicks int
	silentTxTicks int
	deadTicks     int
	state         State
}

func New(target string, thresholdTicks int, minTraffic uint64) *Detector {
	if thresholdTicks < 1 {
		thresholdTicks = 1
	}
	return &Detector{
		target:     target,
		threshold:  thresholdTicks,
		minTraffic: minTraffic,
		state:      StateNormal,
	}
}

func (d *Detector) State() State       { return d.state }
func (d *Detector) SilentRxTicks() int { return d.silentRxTicks }
func (d *Detector) SilentTxTicks() int { return d.silentTxTicks }
func (d *Detector) DeadTicks() int     { return d.deadTicks }

func (d *Detector) Feed(rx, tx uint64) []Alert {
	if d.state == StateDead {
		d.resetCounters()
		d.state = StateNormal
		return []Alert{{Target: d.target, Kind: KindBack, RxDelta: 0, TxDelta: 0, SilentRx: false, SilentTx: false, At: time.Now()}}
	}
	d.deadTicks = 0 // a live sample breaks any 404 streak
	if !d.haveBaseline {
		d.lastRx, d.lastTx, d.haveBaseline = rx, tx, true
		return nil
	}

	rxDelta, rxSilent := d.axisDelta(rx, &d.lastRx)
	txDelta, txSilent := d.axisDelta(tx, &d.lastTx)
	d.lastRx, d.lastTx = rx, tx

	if rxSilent {
		d.silentRxTicks++
	} else {
		d.silentRxTicks = 0
	}
	if txSilent {
		d.silentTxTicks++
	} else {
		d.silentTxTicks = 0
	}

	switch d.state {
	case StateAlerting:
		if !rxSilent && !txSilent {
			d.state = StateNormal
			return []Alert{{Target: d.target, Kind: KindRecovered, RxDelta: rxDelta, TxDelta: txDelta, SilentRx: rxSilent, SilentTx: txSilent, At: time.Now()}}
		}
		return nil
	default: // StateNormal
		if d.silentRxTicks >= d.threshold || d.silentTxTicks >= d.threshold {
			d.state = StateAlerting
			return []Alert{{Target: d.target, Kind: KindAlerted, RxDelta: rxDelta, TxDelta: txDelta, SilentRx: rxSilent, SilentTx: txSilent, At: time.Now()}}
		}
		return nil
	}
}

func (d *Detector) FeedDead() []Alert {
	d.deadTicks++
	if d.state != StateDead && d.deadTicks >= d.threshold {
		d.state = StateDead
		return []Alert{{Target: d.target, Kind: KindDead, RxDelta: 0, TxDelta: 0, SilentRx: false, SilentTx: false, At: time.Now()}}
	}
	return nil
}

// axisDelta returns (delta, silent). A negative delta means the container's
// counters reset (restart): baseline is updated and the tick counts silent.
func (d *Detector) axisDelta(cur uint64, last *uint64) (uint64, bool) {
	if cur < *last {
		*last = cur
		return 0, true
	}
	delta := cur - *last
	return delta, delta <= d.minTraffic
}

func (d *Detector) resetCounters() {
	d.silentRxTicks, d.silentTxTicks, d.deadTicks = 0, 0, 0
}

func (d *Detector) String() string {
	return fmt.Sprintf("target=%s state=%s rxSilent=%d txSilent=%d dead=%d",
		d.target, d.state.String(), d.silentRxTicks, d.silentTxTicks, d.deadTicks)
}
