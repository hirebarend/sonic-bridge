package relay

import (
	"encoding/json"
	"net/http"

	"sonic-bridge/internal/audio"
)

// streamStatus is the observability view of the stream. Format is null when no
// source holds it, which is how a client tells idle from live without a second
// redundant field.
type streamStatus struct {
	IngressAudioBytes   uint64        `json:"ingressAudioBytes"`
	IngressControlBytes uint64        `json:"ingressControlBytes"`
	EgressAudioBytes    uint64        `json:"egressAudioBytes"`
	EgressControlBytes  uint64        `json:"egressControlBytes"`
	WavBytes            uint64        `json:"wavBytes"`
	SuppressedSeconds   float64       `json:"suppressedSeconds"`
	Format              *audio.Format `json:"format"`
	Listeners           int           `json:"listeners"`
	PublishedFrames     uint64        `json:"publishedFrames"`
	DroppedFrames       uint64        `json:"droppedFrames"`
}

func (r *Relay) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte("ok\n"))
}

func (r *Relay) handleStats(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")

	_ = json.NewEncoder(w).Encode(streamStatus{
		Format:              r.stream.FindFormat(),
		IngressAudioBytes:   r.ingressAudioBytes.Load(),
		IngressControlBytes: r.ingressControlBytes.Load(),
		EgressAudioBytes:    r.egressAudioBytes.Load(),
		EgressControlBytes:  r.egressControlBytes.Load(),
		WavBytes:            r.wavBytes.Load(),
		SuppressedSeconds:   r.stream.SuppressedSeconds(),
		Listeners:           r.stream.ListenerCount(),
		PublishedFrames:     r.stream.Published(),
		DroppedFrames:       r.stream.Dropped(),
	})
}
