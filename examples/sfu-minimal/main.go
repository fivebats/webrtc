package main

import (
	"fmt"
	"io"
	"math/rand"
	"time"

	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v2"

	"github.com/pion/webrtc/v2/examples/internal/signal"
)

const (
	rtcpPLIInterval = time.Second * 3
)

func main() {
	sdpChan := signal.HTTPSDPServer()

	// Everything below is the Pion WebRTC API, thanks for using it ❤️.

	// Setup the codecs you want to use.
	// Only support VP8, this makes our proxying code simpler
	offer := webrtc.SessionDescription{}
	signal.Decode(<-sdpChan, &offer)
	fmt.Println("")

	// Only support VP8, this makes our proxying code simpler
	codec, err := preferredCodec(webrtc.VP8, offer)
	if err != nil {
		panic(err)
	}
	vp8Payload := codec.PayloadType
	m := webrtc.MediaEngine{}
	m.RegisterCodec(webrtc.NewRTPVP8Codec(vp8Payload, 90000))

	peerConnectionConfig := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{
				URLs: []string{"stun:stun.l.google.com:19302"},
			},
		},
	}

	// Create the API object with the MediaEngine
	api := webrtc.NewAPI(webrtc.WithMediaEngine(m))
	// Create a new RTCPeerConnection
	peerConnection, err := api.NewPeerConnection(peerConnectionConfig)
	if err != nil {
		panic(err)
	}

	// Allow us to receive 1 video track
	videoTrack, err := peerConnection.NewTrack(vp8Payload, rand.Uint32(), "video", "pion-local")
	if err != nil {
		panic(err)
	}
	_, err = peerConnection.AddTrack(videoTrack)
	if err != nil {
		panic(err)
	}

	localTrackChan := make(chan *webrtc.Track)
	// Set a handler for when a new remote track starts, this just distributes all our packets
	// to connected peers
	peerConnection.OnTrack(func(remoteTrack *webrtc.Track, receiver *webrtc.RTPReceiver) {
		// Send a PLI on an interval so that the publisher is pushing a keyframe every rtcpPLIInterval
		// This can be less wasteful by processing incoming RTCP events, then we would emit a NACK/PLI when a viewer requests it
		go func() {
			ticker := time.NewTicker(rtcpPLIInterval)
			for range ticker.C {
				if rtcpSendErr := peerConnection.WriteRTCP([]rtcp.Packet{&rtcp.PictureLossIndication{MediaSSRC: remoteTrack.SSRC()}}); rtcpSendErr != nil {
					fmt.Println(rtcpSendErr)
				}
			}
		}()

		// Create a local track, all our SFU clients will be fed via this track
		localTrack, newTrackErr := peerConnection.NewTrack(remoteTrack.PayloadType(), remoteTrack.SSRC(), "video", "pion")
		if newTrackErr != nil {
			panic(newTrackErr)
		}
		localTrackChan <- localTrack

		rtpBuf := make([]byte, 1400)
		for {
			i, readErr := remoteTrack.Read(rtpBuf)
			if readErr != nil {
				panic(readErr)
			}

			// ErrClosedPipe means we don't have any subscribers, this is ok if no peers have connected yet
			if _, err = localTrack.Write(rtpBuf[:i]); err != nil && err != io.ErrClosedPipe {
				panic(err)
			}
		}
	})

	// Set the remote SessionDescription
	err = peerConnection.SetRemoteDescription(offer)
	if err != nil {
		panic(err)
	}

	// Create answer
	answer, err := peerConnection.CreateAnswer(nil)
	if err != nil {
		panic(err)
	}

	// Sets the LocalDescription, and starts our UDP listeners
	err = peerConnection.SetLocalDescription(answer)
	if err != nil {
		panic(err)
	}

	//fmt.Printf("ANSWER:\n%s\n", answer.SDP)
	// Get the LocalDescription and take it to base64 so we can paste in browser
	fmt.Println(signal.Encode(answer))

	localTrack := <-localTrackChan
	for {
		// creates peer connection and track for each sfu client
		fmt.Println("")
		fmt.Println("Curl an base64 SDP to start sendonly peer connection")

		recvOnlyOffer := webrtc.SessionDescription{}
		signal.Decode(<-sdpChan, &recvOnlyOffer)

		// payload type for VP8 could be different depending on the client's code
		// so we must follow the type given in the offer
		codec, err := preferredCodec(webrtc.VP8, recvOnlyOffer)
		if err != nil {
			panic(err)
		}
		vp8PayloadType := codec.PayloadType
		m := webrtc.MediaEngine{}
		m.RegisterCodec(webrtc.NewRTPVP8Codec(vp8PayloadType, 90000))

		api := webrtc.NewAPI(webrtc.WithMediaEngine(m))
		// Create a new PeerConnection
		peerConnection, err := api.NewPeerConnection(peerConnectionConfig)
		if err != nil {
			panic(err)
		}

		_, err = peerConnection.AddTrack(localTrack)
		if err != nil {
			panic(err)
		}

		// Set the remote SessionDescription
		err = peerConnection.SetRemoteDescription(recvOnlyOffer)
		if err != nil {
			panic(err)
		}

		// Create answer
		answer, err := peerConnection.CreateAnswer(nil)
		if err != nil {
			panic(err)
		}

		// Sets the LocalDescription, and starts our UDP listeners
		err = peerConnection.SetLocalDescription(answer)
		if err != nil {
			panic(err)
		}

		// Get the LocalDescription and take it to base64 so we can paste in browser
		fmt.Println(signal.Encode(answer))
	}
}

// preferredCodec returns the preferred codec for a given name (such as "VP8") found
// in the sample description
func preferredCodec(codecName string, sd webrtc.SessionDescription) (*webrtc.RTPCodec, error) {
	m := webrtc.MediaEngine{}
	err := m.PopulateFromSDP(sd)
	if err != nil {
		return nil, err
	}
	codecs := m.GetCodecsByName(codecName)
	if len(codecs) == 0 {
		return nil, fmt.Errorf("no %d codec in session description", codecName)
	}
	return codecs[0], nil
}
