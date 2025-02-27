package webrtc

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/bluenviron/gortsplib/v4/pkg/description"
	"github.com/bluenviron/gortsplib/v4/pkg/format"
	"github.com/bluenviron/gortsplib/v4/pkg/format/rtpav1"
	"github.com/bluenviron/gortsplib/v4/pkg/format/rtph264"
	"github.com/bluenviron/gortsplib/v4/pkg/format/rtpvp8"
	"github.com/bluenviron/gortsplib/v4/pkg/format/rtpvp9"
	"github.com/bluenviron/gortsplib/v4/pkg/rtptime"
	"github.com/google/uuid"
	"github.com/pion/sdp/v3"
	pwebrtc "github.com/pion/webrtc/v3"

	"github.com/bluenviron/mediamtx/internal/asyncwriter"
	"github.com/bluenviron/mediamtx/internal/defs"
	"github.com/bluenviron/mediamtx/internal/externalcmd"
	"github.com/bluenviron/mediamtx/internal/hooks"
	"github.com/bluenviron/mediamtx/internal/logger"
	"github.com/bluenviron/mediamtx/internal/protocols/webrtc"
	"github.com/bluenviron/mediamtx/internal/stream"
	"github.com/bluenviron/mediamtx/internal/unit"
)

type setupStreamFunc func(*webrtc.OutgoingTrack) error

func findVideoTrack(
	stream *stream.Stream,
	writer *asyncwriter.Writer,
) (format.Format, setupStreamFunc) {
	var av1Format *format.AV1
	media := stream.Desc().FindFormat(&av1Format)

	if av1Format != nil {
		return av1Format, func(track *webrtc.OutgoingTrack) error {
			encoder := &rtpav1.Encoder{
				PayloadType:    105,
				PayloadMaxSize: webrtcPayloadMaxSize,
			}
			err := encoder.Init()
			if err != nil {
				return err
			}

			stream.AddReader(writer, media, av1Format, func(u unit.Unit) error {
				tunit := u.(*unit.AV1)

				if tunit.TU == nil {
					return nil
				}

				packets, err := encoder.Encode(tunit.TU)
				if err != nil {
					return nil //nolint:nilerr
				}

				for _, pkt := range packets {
					pkt.Timestamp += tunit.RTPPackets[0].Timestamp
					track.WriteRTP(pkt) //nolint:errcheck
				}

				return nil
			})

			return nil
		}
	}

	var vp9Format *format.VP9
	media = stream.Desc().FindFormat(&vp9Format)

	if vp9Format != nil {
		return vp9Format, func(track *webrtc.OutgoingTrack) error {
			encoder := &rtpvp9.Encoder{
				PayloadType:    96,
				PayloadMaxSize: webrtcPayloadMaxSize,
			}
			err := encoder.Init()
			if err != nil {
				return err
			}

			stream.AddReader(writer, media, vp9Format, func(u unit.Unit) error {
				tunit := u.(*unit.VP9)

				if tunit.Frame == nil {
					return nil
				}

				packets, err := encoder.Encode(tunit.Frame)
				if err != nil {
					return nil //nolint:nilerr
				}

				for _, pkt := range packets {
					pkt.Timestamp += tunit.RTPPackets[0].Timestamp
					track.WriteRTP(pkt) //nolint:errcheck
				}

				return nil
			})

			return nil
		}
	}

	var vp8Format *format.VP8
	media = stream.Desc().FindFormat(&vp8Format)

	if vp8Format != nil {
		return vp8Format, func(track *webrtc.OutgoingTrack) error {
			encoder := &rtpvp8.Encoder{
				PayloadType:    96,
				PayloadMaxSize: webrtcPayloadMaxSize,
			}
			err := encoder.Init()
			if err != nil {
				return err
			}

			stream.AddReader(writer, media, vp8Format, func(u unit.Unit) error {
				tunit := u.(*unit.VP8)

				if tunit.Frame == nil {
					return nil
				}

				packets, err := encoder.Encode(tunit.Frame)
				if err != nil {
					return nil //nolint:nilerr
				}

				for _, pkt := range packets {
					pkt.Timestamp += tunit.RTPPackets[0].Timestamp
					track.WriteRTP(pkt) //nolint:errcheck
				}

				return nil
			})

			return nil
		}
	}

	var h264Format *format.H264
	media = stream.Desc().FindFormat(&h264Format)

	if h264Format != nil {
		return h264Format, func(track *webrtc.OutgoingTrack) error {
			encoder := &rtph264.Encoder{
				PayloadType:    96,
				PayloadMaxSize: webrtcPayloadMaxSize,
			}
			err := encoder.Init()
			if err != nil {
				return err
			}

			firstReceived := false
			var lastPTS time.Duration

			stream.AddReader(writer, media, h264Format, func(u unit.Unit) error {
				tunit := u.(*unit.H264)

				if tunit.AU == nil {
					return nil
				}

				if !firstReceived {
					firstReceived = true
				} else if tunit.PTS < lastPTS {
					return fmt.Errorf("WebRTC doesn't support H264 streams with B-frames")
				}
				lastPTS = tunit.PTS

				packets, err := encoder.Encode(tunit.AU)
				if err != nil {
					return nil //nolint:nilerr
				}

				for _, pkt := range packets {
					pkt.Timestamp += tunit.RTPPackets[0].Timestamp
					track.WriteRTP(pkt) //nolint:errcheck
				}

				return nil
			})

			return nil
		}
	}

	return nil, nil
}

func findAudioTrack(
	stream *stream.Stream,
	writer *asyncwriter.Writer,
) (format.Format, setupStreamFunc) {
	var opusFormat *format.Opus
	media := stream.Desc().FindFormat(&opusFormat)

	if opusFormat != nil {
		return opusFormat, func(track *webrtc.OutgoingTrack) error {
			stream.AddReader(writer, media, opusFormat, func(u unit.Unit) error {
				for _, pkt := range u.GetRTPPackets() {
					track.WriteRTP(pkt) //nolint:errcheck
				}

				return nil
			})
			return nil
		}
	}

	var g722Format *format.G722
	media = stream.Desc().FindFormat(&g722Format)

	if g722Format != nil {
		return g722Format, func(track *webrtc.OutgoingTrack) error {
			stream.AddReader(writer, media, g722Format, func(u unit.Unit) error {
				for _, pkt := range u.GetRTPPackets() {
					track.WriteRTP(pkt) //nolint:errcheck
				}

				return nil
			})
			return nil
		}
	}

	var g711Format *format.G711
	media = stream.Desc().FindFormat(&g711Format)

	if g711Format != nil {
		return g711Format, func(track *webrtc.OutgoingTrack) error {
			if g711Format.SampleRate != 8000 {
				return fmt.Errorf("unsupported G711 sample rate")
			}

			if g711Format.ChannelCount != 1 {
				return fmt.Errorf("unsupported G711 channel count")
			}

			stream.AddReader(writer, media, g711Format, func(u unit.Unit) error {
				for _, pkt := range u.GetRTPPackets() {
					track.WriteRTP(pkt) //nolint:errcheck
				}

				return nil
			})
			return nil
		}
	}

	return nil, nil
}

func whipOffer(body []byte) *pwebrtc.SessionDescription {
	return &pwebrtc.SessionDescription{
		Type: pwebrtc.SDPTypeOffer,
		SDP:  string(body),
	}
}

type session struct {
	parentCtx       context.Context
	writeQueueSize  int
	api             *pwebrtc.API
	req             webRTCNewSessionReq
	wg              *sync.WaitGroup
	externalCmdPool *externalcmd.Pool
	pathManager     defs.PathManager
	parent          *Server

	ctx       context.Context
	ctxCancel func()
	created   time.Time
	uuid      uuid.UUID
	secret    uuid.UUID
	mutex     sync.RWMutex
	pc        *webrtc.PeerConnection

	chNew           chan webRTCNewSessionReq
	chAddCandidates chan webRTCAddSessionCandidatesReq
}

func (s *session) initialize() {
	ctx, ctxCancel := context.WithCancel(s.parentCtx)

	s.ctx = ctx
	s.ctxCancel = ctxCancel
	s.created = time.Now()
	s.uuid = uuid.New()
	s.secret = uuid.New()
	s.chNew = make(chan webRTCNewSessionReq)
	s.chAddCandidates = make(chan webRTCAddSessionCandidatesReq)

	s.Log(logger.Info, "created by %s", s.req.remoteAddr)

	s.wg.Add(1)
	go s.run()
}

// Log implements logger.Writer.
func (s *session) Log(level logger.Level, format string, args ...interface{}) {
	id := hex.EncodeToString(s.uuid[:4])
	s.parent.Log(level, "[session %v] "+format, append([]interface{}{id}, args...)...)
}

func (s *session) Close() {
	s.ctxCancel()
}

func (s *session) run() {
	defer s.wg.Done()

	err := s.runInner()

	s.ctxCancel()

	s.parent.closeSession(s)

	s.Log(logger.Info, "closed: %v", err)
}

func (s *session) runInner() error {
	select {
	case <-s.chNew:
	case <-s.ctx.Done():
		return fmt.Errorf("terminated")
	}

	errStatusCode, err := s.runInner2()

	if errStatusCode != 0 {
		s.req.res <- webRTCNewSessionRes{
			errStatusCode: errStatusCode,
			err:           err,
		}
	}

	return err
}

func (s *session) runInner2() (int, error) {
	if s.req.publish {
		return s.runPublish()
	}
	return s.runRead()
}

func (s *session) runPublish() (int, error) {
	ip, _, _ := net.SplitHostPort(s.req.remoteAddr)

	res := s.pathManager.AddPublisher(defs.PathAddPublisherReq{
		Author: s,
		AccessRequest: defs.PathAccessRequest{
			Name:    s.req.pathName,
			Query:   s.req.query,
			Publish: true,
			IP:      net.ParseIP(ip),
			User:    s.req.user,
			Pass:    s.req.pass,
			Proto:   defs.AuthProtocolWebRTC,
			ID:      &s.uuid,
		},
	})
	if res.Err != nil {
		var terr defs.AuthenticationError
		if errors.As(res.Err, &terr) {
			// wait some seconds to mitigate brute force attacks
			<-time.After(pauseAfterAuthError)

			return http.StatusUnauthorized, res.Err
		}

		return http.StatusBadRequest, res.Err
	}

	defer res.Path.RemovePublisher(defs.PathRemovePublisherReq{Author: s})

	iceServers, err := s.parent.generateICEServers()
	if err != nil {
		return http.StatusInternalServerError, err
	}

	pc := &webrtc.PeerConnection{
		ICEServers: iceServers,
		API:        s.api,
		Publish:    false,
		Log:        s,
	}
	err = pc.Start()
	if err != nil {
		return http.StatusBadRequest, err
	}
	defer pc.Close()

	offer := whipOffer(s.req.offer)

	var sdp sdp.SessionDescription
	err = sdp.Unmarshal([]byte(offer.SDP))
	if err != nil {
		return http.StatusBadRequest, err
	}

	trackCount, err := webrtc.TrackCount(sdp.MediaDescriptions)
	if err != nil {
		// RFC draft-ietf-wish-whip
		// if the number of audio and or video
		// tracks or number streams is not supported by the WHIP Endpoint, it
		// MUST reject the HTTP POST request with a "406 Not Acceptable" error
		// response.
		return http.StatusNotAcceptable, err
	}

	answer, err := pc.CreateFullAnswer(s.ctx, offer)
	if err != nil {
		return http.StatusBadRequest, err
	}

	s.writeAnswer(answer)

	go s.readRemoteCandidates(pc)

	err = pc.WaitUntilConnected(s.ctx)
	if err != nil {
		return 0, err
	}

	s.mutex.Lock()
	s.pc = pc
	s.mutex.Unlock()

	tracks, err := pc.GatherIncomingTracks(s.ctx, trackCount)
	if err != nil {
		return 0, err
	}

	medias := webrtc.TracksToMedias(tracks)

	rres := res.Path.StartPublisher(defs.PathStartPublisherReq{
		Author:             s,
		Desc:               &description.Session{Medias: medias},
		GenerateRTPPackets: false,
	})
	if rres.Err != nil {
		return 0, rres.Err
	}

	timeDecoder := rtptime.NewGlobalDecoder()

	for i, media := range medias {
		ci := i
		cmedia := media
		trackWrapper := &webrtc.TrackWrapper{ClockRat: cmedia.Formats[0].ClockRate()}

		go func() {
			for {
				pkt, err := tracks[ci].ReadRTP()
				if err != nil {
					return
				}

				pts, ok := timeDecoder.Decode(trackWrapper, pkt)
				if !ok {
					continue
				}

				rres.Stream.WriteRTPPacket(cmedia, cmedia.Formats[0], pkt, time.Now(), pts)
			}
		}()
	}

	select {
	case <-pc.Disconnected():
		return 0, fmt.Errorf("peer connection closed")

	case <-s.ctx.Done():
		return 0, fmt.Errorf("terminated")
	}
}

func (s *session) runRead() (int, error) {
	ip, _, _ := net.SplitHostPort(s.req.remoteAddr)

	res := s.pathManager.AddReader(defs.PathAddReaderReq{
		Author: s,
		AccessRequest: defs.PathAccessRequest{
			Name:  s.req.pathName,
			Query: s.req.query,
			IP:    net.ParseIP(ip),
			User:  s.req.user,
			Pass:  s.req.pass,
			Proto: defs.AuthProtocolWebRTC,
			ID:    &s.uuid,
		},
	})
	if res.Err != nil {
		var terr defs.AuthenticationError
		if errors.As(res.Err, &terr) {
			// wait some seconds to mitigate brute force attacks
			<-time.After(pauseAfterAuthError)
			return http.StatusUnauthorized, res.Err
		}

		if strings.HasPrefix(res.Err.Error(), "no one is publishing") {
			return http.StatusNotFound, res.Err
		}

		return http.StatusBadRequest, res.Err
	}

	defer res.Path.RemoveReader(defs.PathRemoveReaderReq{Author: s})

	iceServers, err := s.parent.generateICEServers()
	if err != nil {
		return http.StatusInternalServerError, err
	}

	pc := &webrtc.PeerConnection{
		ICEServers: iceServers,
		API:        s.api,
		Publish:    false,
		Log:        s,
	}
	err = pc.Start()
	if err != nil {
		return http.StatusBadRequest, err
	}
	defer pc.Close()

	writer := asyncwriter.New(s.writeQueueSize, s)

	videoTrack, videoSetup := findVideoTrack(res.Stream, writer)
	audioTrack, audioSetup := findAudioTrack(res.Stream, writer)

	if videoTrack == nil && audioTrack == nil {
		return http.StatusBadRequest, fmt.Errorf(
			"the stream doesn't contain any supported codec, which are currently AV1, VP9, VP8, H264, Opus, G722, G711")
	}

	tracks, err := pc.SetupOutgoingTracks(videoTrack, audioTrack)
	if err != nil {
		return http.StatusBadRequest, err
	}

	offer := whipOffer(s.req.offer)

	answer, err := pc.CreateFullAnswer(s.ctx, offer)
	if err != nil {
		return http.StatusBadRequest, err
	}

	s.writeAnswer(answer)

	go s.readRemoteCandidates(pc)

	err = pc.WaitUntilConnected(s.ctx)
	if err != nil {
		return 0, err
	}

	s.mutex.Lock()
	s.pc = pc
	s.mutex.Unlock()

	defer res.Stream.RemoveReader(writer)

	n := 0

	if videoTrack != nil {
		err := videoSetup(tracks[n])
		if err != nil {
			return 0, err
		}
		n++
	}

	if audioTrack != nil {
		err := audioSetup(tracks[n])
		if err != nil {
			return 0, err
		}
	}

	s.Log(logger.Info, "is reading from path '%s', %s",
		res.Path.Name(), defs.FormatsInfo(res.Stream.FormatsForReader(writer)))

	onUnreadHook := hooks.OnRead(hooks.OnReadParams{
		Logger:          s,
		ExternalCmdPool: s.externalCmdPool,
		Conf:            res.Path.SafeConf(),
		ExternalCmdEnv:  res.Path.ExternalCmdEnv(),
		Reader:          s.APIReaderDescribe(),
		Query:           s.req.query,
	})
	defer onUnreadHook()

	writer.Start()

	select {
	case <-pc.Disconnected():
		writer.Stop()
		return 0, fmt.Errorf("peer connection closed")

	case err := <-writer.Error():
		return 0, err

	case <-s.ctx.Done():
		writer.Stop()
		return 0, fmt.Errorf("terminated")
	}
}

func (s *session) writeAnswer(answer *pwebrtc.SessionDescription) {
	s.req.res <- webRTCNewSessionRes{
		sx:     s,
		answer: []byte(answer.SDP),
	}
}

func (s *session) readRemoteCandidates(pc *webrtc.PeerConnection) {
	for {
		select {
		case req := <-s.chAddCandidates:
			for _, candidate := range req.candidates {
				err := pc.AddRemoteCandidate(*candidate)
				if err != nil {
					req.res <- webRTCAddSessionCandidatesRes{err: err}
				}
			}
			req.res <- webRTCAddSessionCandidatesRes{}

		case <-s.ctx.Done():
			return
		}
	}
}

// new is called by webRTCHTTPServer through Server.
func (s *session) new(req webRTCNewSessionReq) webRTCNewSessionRes {
	select {
	case s.chNew <- req:
		return <-req.res

	case <-s.ctx.Done():
		return webRTCNewSessionRes{err: fmt.Errorf("terminated"), errStatusCode: http.StatusInternalServerError}
	}
}

// addCandidates is called by webRTCHTTPServer through Server.
func (s *session) addCandidates(
	req webRTCAddSessionCandidatesReq,
) webRTCAddSessionCandidatesRes {
	select {
	case s.chAddCandidates <- req:
		return <-req.res

	case <-s.ctx.Done():
		return webRTCAddSessionCandidatesRes{err: fmt.Errorf("terminated")}
	}
}

// APIReaderDescribe implements reader.
func (s *session) APIReaderDescribe() defs.APIPathSourceOrReader {
	return defs.APIPathSourceOrReader{
		Type: "webrtcSession",
		ID:   s.uuid.String(),
	}
}

// APISourceDescribe implements source.
func (s *session) APISourceDescribe() defs.APIPathSourceOrReader {
	return s.APIReaderDescribe()
}

func (s *session) apiItem() *defs.APIWebRTCSession {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	peerConnectionEstablished := false
	localCandidate := ""
	remoteCandidate := ""
	bytesReceived := uint64(0)
	bytesSent := uint64(0)

	if s.pc != nil {
		peerConnectionEstablished = true
		localCandidate = s.pc.LocalCandidate()
		remoteCandidate = s.pc.RemoteCandidate()
		bytesReceived = s.pc.BytesReceived()
		bytesSent = s.pc.BytesSent()
	}

	return &defs.APIWebRTCSession{
		ID:                        s.uuid,
		Created:                   s.created,
		RemoteAddr:                s.req.remoteAddr,
		PeerConnectionEstablished: peerConnectionEstablished,
		LocalCandidate:            localCandidate,
		RemoteCandidate:           remoteCandidate,
		State: func() defs.APIWebRTCSessionState {
			if s.req.publish {
				return defs.APIWebRTCSessionStatePublish
			}
			return defs.APIWebRTCSessionStateRead
		}(),
		Path:          s.req.pathName,
		Query:         s.req.query,
		BytesReceived: bytesReceived,
		BytesSent:     bytesSent,
	}
}
