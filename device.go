package main

import "zee/audio"

func selectDevice(ctx audio.Context) (*audio.DeviceInfo, error) {
	return audio.SelectDevice(ctx)
}
