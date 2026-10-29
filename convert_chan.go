package qngg

import "reflect"

// channelToJSObjectValue converts a Go channel to a JavaScript object with async methods.
func channelToJSObjectValue(
	rt *Runtime,
	rtype reflect.Type,
	rval reflect.Value,
) (*Value, error) {
	if rval.IsNil() {
		return rt.NewNull(), nil
	}

	return withJSObject(rt, func(obj *Value) error {
		// Check channel direction
		chanDir := rtype.ChanDir()
		canSend := chanDir == reflect.BothDir || chanDir == reflect.SendDir
		canRecv := chanDir == reflect.BothDir || chanDir == reflect.RecvDir
		objMethods := map[string]any{
			"close":    createChannelCloseFunc(rval),
			"length":   rval.Len,
			"capacity": rval.Cap,
		}

		for name, value := range map[string]*Value{
			"type":        rt.NewString("channel"),
			"elementType": rt.NewString(goTypeName(rtype.Elem())),
			"canSend":     rt.NewBool(canSend),
			"canReceive":  rt.NewBool(canRecv),
		} {
			if err := setTake(obj, name, value); err != nil {
				return err
			}
		}

		if canSend {
			objMethods["send"] = createChannelSendFunc(rval)
		}

		if canRecv {
			objMethods["receive"] = createChannelReceiveFunc(rval)
		}

		for name, method := range objMethods {
			// Skip error check since we know the function signatures are correct
			jsMethod, _ := funcToJS(rt, method)
			if err := setTake(obj, name, jsMethod); err != nil {
				return err
			}
		}

		return nil
	})
}

// createChannelSendFunc creates 'func(T) error' for sending T to a channel.
func createChannelSendFunc(chanRValue reflect.Value) any {
	chanElemRType := chanRValue.Type().Elem()
	funcRType := reflect.FuncOf(
		[]reflect.Type{chanElemRType}, // params
		[]reflect.Type{errRType},      // returns
		false,                         // variadic
	)

	fn := reflect.MakeFunc(funcRType, func(args []reflect.Value) []reflect.Value {
		valueToSend := args[0]
		errValue := errZeroRValue
		chosen, _, _ := reflect.Select([]reflect.SelectCase{
			{Dir: reflect.SelectSend, Chan: chanRValue, Send: valueToSend},
			{Dir: reflect.SelectDefault},
		})

		// Default case was chosen - send would block
		if chosen == 1 {
			errValue = reflect.ValueOf(errChanSend)
		}

		return []reflect.Value{errValue}
	})

	// Successful send
	return fn.Interface()
}

// createChannelReceiveFunc creates a function for receiving values from a channel.
func createChannelReceiveFunc(chanRValue reflect.Value) func() (any, error) {
	return func() (any, error) {
		chosen, recv, ok := reflect.Select([]reflect.SelectCase{
			{Dir: reflect.SelectRecv, Chan: chanRValue},
			{Dir: reflect.SelectDefault},
		})

		// First: Check default case was chosen - no data available
		if chosen == 1 {
			return nil, errChanReceive
		}

		// Receive case was chosen but channel is closed
		if !ok {
			return nil, errChanClosed
		}

		// Successful receive
		return recv.Interface(), nil
	}
}

// createChannelCloseFunc creates a function for closing a channel.
func createChannelCloseFunc(rval reflect.Value) func() error {
	return func() error {
		// Only close if it's a send or bidirectional channel
		if rval.Type().ChanDir() == reflect.RecvDir {
			return errChanCloseReceiveOnly
		}

		rval.Close()

		return nil
	}
}
