package formatprocessor //nolint:dupl

import (
	"errors"
	"fmt"
	"time"

	"github.com/bluenviron/gortsplib/v4/pkg/format"
	"github.com/bluenviron/gortsplib/v4/pkg/format/rtpav1"
	"github.com/pion/rtp"

	"github.com/bluenviron/mediamtx/internal/unit"
)

type formatProcessorAV1 struct {
	udpMaxPayloadSize int
	format            *format.AV1

	encoder *rtpav1.Encoder
	decoder *rtpav1.Decoder
}

func newAV1(
	udpMaxPayloadSize int,
	forma *format.AV1,
	generateRTPPackets bool,
) (*formatProcessorAV1, error) {
	t := &formatProcessorAV1{
		udpMaxPayloadSize: udpMaxPayloadSize,
		format:            forma,
	}

	if generateRTPPackets {
		err := t.createEncoder()
		if err != nil {
			return nil, err
		}
	}

	return t, nil
}

func (t *formatProcessorAV1) createEncoder() error {
	t.encoder = &rtpav1.Encoder{
		PayloadMaxSize: t.udpMaxPayloadSize - 12,
		PayloadType:    t.format.PayloadTyp,
	}
	return t.encoder.Init()
}

func (t *formatProcessorAV1) ProcessUnit(uu unit.Unit) error { //nolint:dupl
	u := uu.(*unit.AV1)

	pkts, err := t.encoder.Encode(u.TU)
	if err != nil {
		return err
	}
	u.RTPPackets = pkts

	ts := uint32(multiplyAndDivide(u.PTS, time.Duration(t.format.ClockRate()), time.Second))
	for _, pkt := range u.RTPPackets {
		pkt.Timestamp += ts
	}

	return nil
}

func (t *formatProcessorAV1) ProcessRTPPacket( //nolint:dupl
	pkt *rtp.Packet,
	ntp time.Time,
	pts time.Duration,
	hasNonRTSPReaders bool,
) (Unit, error) {
	u := &unit.AV1{
		Base: unit.Base{
			RTPPackets: []*rtp.Packet{pkt},
			NTP:        ntp,
			PTS:        pts,
		},
	}

	// remove padding
	pkt.Header.Padding = false
	pkt.PaddingSize = 0

	if pkt.MarshalSize() > t.udpMaxPayloadSize {
		return nil, fmt.Errorf("payload size (%d) is greater than maximum allowed (%d)",
			pkt.MarshalSize(), t.udpMaxPayloadSize)
	}

	// decode from RTP
	if hasNonRTSPReaders || t.decoder != nil {
		if t.decoder == nil {
			var err error
			t.decoder, err = t.format.CreateDecoder()
			if err != nil {
				return nil, err
			}
		}

		tu, err := t.decoder.Decode(pkt)
		if err != nil {
			if errors.Is(err, rtpav1.ErrNonStartingPacketAndNoPrevious) ||
				errors.Is(err, rtpav1.ErrMorePacketsNeeded) {
				return u, nil
			}
			return nil, err
		}

		u.TU = tu
	}

	// route packet as is
	return u, nil
}
