package server

import (
	"fmt"
	"reflect"
	"testing"
)

func TestNewBasicServer(t *testing.T) {
	s := NewBasicServer(nil)
	basicServer, ok := s.(*basicServer)
	if ok {
		fmt.Println(reflect.TypeOf(basicServer))
	}
	fmt.Println(basicServer.DiscoverType())
}
