package main

import (
	"bytes"
	"encoding/base64"
	"encoding/gob"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/boltdb/bolt"
	"github.com/continusec/go-client/continusec"
	"github.com/urfave/cli"
)

func showStatus(db *bolt.DB, c *cli.Context) error {
	mapState, err := getCurrentHead("head")
	if err != nil {
		return err
	}

	if mapState == nil {
		fmt.Printf("Empty map.\n")
	} else {
		fmt.Printf("Tracking revision: %d\n", mapState.TreeSize())
	}

	return nil
}

type gossip struct {
	Signature           []byte `json:"sig"`
	TreeHeadLogTreehead []byte `json:"thlth"`
}

func showGossip(db *bolt.DB, c *cli.Context) error {
	mapState, err := getCurrentHead("head")
	if err != nil {
		return err
	}

	if mapState == nil {
		return cli.NewExitError("empty map, nothing to gossip", 1)
	}

	server, err := getServer()
	if err != nil {
		return err
	}

	ce, err := (&CachingVerifyingRT{DB: db}).getValFromCache(fmt.Sprintf("%s/v1/wrappedMap/log/treehead/tree/%d", server, mapState.TreeHeadLogTreeHead.TreeSize))
	if err != nil {
		return err
	}

	buffer := &bytes.Buffer{}
	err = json.NewEncoder(buffer).Encode(&gossip{Signature: ce.Signature, TreeHeadLogTreehead: ce.Data})
	if err != nil {
		return err
	}

	fmt.Println(base64.StdEncoding.EncodeToString(buffer.Bytes()))

	return nil
}

func updateTree(db *bolt.DB, c *cli.Context) error {
	seq := 0
	switch c.NArg() {
	case 0:
		seq = 0
	case 1:
		var err error
		seq, err = strconv.Atoi(c.Args().Get(0))
		if err != nil {
			return err
		}
	default:
		return cli.NewExitError("wrong number of arguments specified", 1)
	}

	mapState, err := getCurrentHead("head")
	if err != nil {
		return err
	}

	vmap, err := getMap()
	if err != nil {
		return err
	}

	newMapState, err := vmap.VerifiedMapState(mapState, int64(seq))
	if err != nil {
		return err
	}

	if newMapState != nil {
		// check for any pending updates
		err = checkUpdateListForNewness(db, newMapState)
		if err != nil {
			return err
		}

		// update any keys we watch
		err = updateKeysToMapState(db, newMapState)
		if err != nil {
			return err
		}

		err = setCurrentHead("head", newMapState)
		if err != nil {
			return err
		}
	}

	return showStatus(db, c)
}

func setCurrentHead(key string, newMapState *continusec.MapTreeState) error {
	db, err := GetDB()
	if err != nil {
		return err
	}

	b := &bytes.Buffer{}
	err = gob.NewEncoder(b).Encode(newMapState)
	if err != nil {
		return err
	}

	return db.Update(func(tx *bolt.Tx) error {
		err = tx.Bucket([]byte("conf")).Delete([]byte("nil" + key + "ok"))
		if err != nil {
			return err
		}
		return tx.Bucket([]byte("conf")).Put([]byte(key), b.Bytes())
	})
}

var (
	ErrMissingHead = errors.New("ErrMissingHead")
)

func getCurrentHead(key string) (*continusec.MapTreeState, error) {
	var mapState continusec.MapTreeState
	var empty bool

	db, err := GetDB()
	if err != nil {
		return nil, err
	}

	err = db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("conf")).Get([]byte(key))
		if len(b) == 0 {
			if bytes.Equal(tx.Bucket([]byte("conf")).Get([]byte("nil"+key+"ok")), []byte{1}) {
				empty = true
				return nil
			} else {
				return ErrMissingHead
			}
		} else {
			return gob.NewDecoder(bytes.NewReader(b)).Decode(&mapState)
		}
	})
	if err != nil {
		return nil, err
	}

	if empty {
		return nil, nil
	} else {
		return &mapState, nil
	}
}