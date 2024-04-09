package httphandlers

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/micvbang/go-helpy/uint64y"
	"github.com/micvbang/simple-event-broker/internal/infrastructure/logger"
	"github.com/micvbang/simple-event-broker/internal/recordbatch"
	"github.com/micvbang/simple-event-broker/internal/storage"
)

type RecordGetter interface {
	GetRecord(topicName string, recordID uint64) (recordbatch.Record, error)
}

func GetRecord(log logger.Logger, s RecordGetter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		log.Debugf("hit %s", r.URL)

		params, err := parseQueryParams(r, []string{recordIDKey, topicNameKey})
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, err.Error())
		}

		recordID, err := uint64y.FromString(params[recordIDKey])
		if err != nil {
			log.Errorf("parsing record id key: %s", err.Error())
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, "url parameter '%s', must be a number: %s", recordIDKey, err)
			w.Write([]byte(err.Error()))
			return
		}

		record, err := s.GetRecord(params[topicNameKey], recordID)
		if err != nil {
			if errors.Is(err, storage.ErrOutOfBounds) {
				log.Debugf("not found")
				w.WriteHeader(http.StatusNotFound)
				return
			}

			log.Errorf("reading record: %s", err.Error())
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprintf(w, "failed to read record '%d': %s", recordID, err)
		}
		w.Write(record)
	}
}
