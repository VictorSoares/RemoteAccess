//go:build windows

package audio

import (
	"context"
	"encoding/binary"
	"log"
	"syscall"
	"time"
	"unsafe"
)

var (
	ole32                = syscall.NewLazyDLL("ole32.dll")
	procCoInitializeEx   = ole32.NewProc("CoInitializeEx")
	procCoUninitialize   = ole32.NewProc("CoUninitialize")
	procCoCreateInstance = ole32.NewProc("CoCreateInstance")
)

type GUID struct {
	Data1 uint32
	Data2 uint16
	Data3 uint16
	Data4 [8]byte
}

var (
	CLSID_MMDeviceEnumerator = GUID{0xBCDE0395, 0xE52F, 0x467C, [8]byte{0x8E, 0x3D, 0xC4, 0x57, 0x92, 0x91, 0x69, 0x2E}}
	IID_IMMDeviceEnumerator  = GUID{0xA95664D2, 0x9614, 0x4F35, [8]byte{0xA7, 0x46, 0xDE, 0x8D, 0xB6, 0x36, 0x17, 0xE6}}
	IID_IAudioClient         = GUID{0x1CB9AD4C, 0xDBFA, 0x4C32, [8]byte{0xB1, 0x78, 0xC2, 0xF5, 0x68, 0xA7, 0x03, 0xB2}}
	IID_IAudioCaptureClient  = GUID{0xC8ADBD64, 0xE71E, 0x48A0, [8]byte{0xA4, 0xDE, 0x18, 0x5C, 0x39, 0x5C, 0xD3, 0x17}}
)

const (
	CLSCTX_ALL                  = 23
	COINIT_MULTITHREADED        = 0x0
	eRender                     = 0
	eConsole                    = 0
	AUDCLNT_SHAREMODE_SHARED    = 0
	AUDCLNT_STREAMFLAGS_LOOPBACK = 0x00020000
	AUDCLNT_BUFFERFLAGS_SILENT  = 0x00000002
)

type waveFormatEx struct {
	wFormatTag      uint16
	nChannels       uint16
	nSamplesPerSec  uint32
	nAvgBytesPerSec uint32
	nBlockAlign     uint16
	wBitsPerSample  uint16
	cbSize          uint16
}

// StartLoopbackCapture starts capturing Windows default audio output via WASAPI loopback.
// It delivers chunks of 16-bit stereo PCM audio (at 48000 Hz) to the onPCM callback.
// Capture stops when ctx is cancelled.
func StartLoopbackCapture(ctx context.Context, onPCM func(data []byte)) error {
	go func() {
		procCoInitializeEx.Call(0, uintptr(COINIT_MULTITHREADED))
		defer procCoUninitialize.Call()

		var enumerator uintptr
		hr, _, _ := procCoCreateInstance.Call(
			uintptr(unsafe.Pointer(&CLSID_MMDeviceEnumerator)),
			0,
			uintptr(CLSCTX_ALL),
			uintptr(unsafe.Pointer(&IID_IMMDeviceEnumerator)),
			uintptr(unsafe.Pointer(&enumerator)),
		)
		if hr != 0 || enumerator == 0 {
			log.Printf("[Audio] CoCreateInstance MMDeviceEnumerator falhou (0x%x)", hr)
			return
		}
		enumVtbl := *(*[]uintptr)(unsafe.Pointer(&struct {
			addr uintptr
			len  int
			cap  int
		}{addr: *(*uintptr)(unsafe.Pointer(enumerator)), len: 10, cap: 10}))

		var device uintptr
		hr, _, _ = syscall.SyscallN(enumVtbl[4], enumerator, uintptr(eRender), uintptr(eConsole), uintptr(unsafe.Pointer(&device)))
		if hr != 0 || device == 0 {
			log.Printf("[Audio] GetDefaultAudioEndpoint falhou (0x%x)", hr)
			return
		}
		devVtbl := *(*[]uintptr)(unsafe.Pointer(&struct {
			addr uintptr
			len  int
			cap  int
		}{addr: *(*uintptr)(unsafe.Pointer(device)), len: 10, cap: 10}))

		var audioClient uintptr
		hr, _, _ = syscall.SyscallN(devVtbl[3], device, uintptr(unsafe.Pointer(&IID_IAudioClient)), uintptr(CLSCTX_ALL), 0, uintptr(unsafe.Pointer(&audioClient)))
		if hr != 0 || audioClient == 0 {
			log.Printf("[Audio] Activate IAudioClient falhou (0x%x)", hr)
			return
		}
		acVtbl := *(*[]uintptr)(unsafe.Pointer(&struct {
			addr uintptr
			len  int
			cap  int
		}{addr: *(*uintptr)(unsafe.Pointer(audioClient)), len: 25, cap: 25}))

		var pwfx *waveFormatEx
		hr, _, _ = syscall.SyscallN(acVtbl[8], audioClient, uintptr(unsafe.Pointer(&pwfx)))
		if hr != 0 || pwfx == nil {
			log.Printf("[Audio] GetMixFormat falhou (0x%x)", hr)
			return
		}

		// Initialize with 100ms buffer
		hr, _, _ = syscall.SyscallN(acVtbl[3], audioClient, uintptr(AUDCLNT_SHAREMODE_SHARED), uintptr(AUDCLNT_STREAMFLAGS_LOOPBACK), 1000000, 0, uintptr(unsafe.Pointer(pwfx)), 0)
		if hr != 0 {
			log.Printf("[Audio] AudioClient Initialize falhou (0x%x)", hr)
			return
		}

		var captureClient uintptr
		hr, _, _ = syscall.SyscallN(acVtbl[14], audioClient, uintptr(unsafe.Pointer(&IID_IAudioCaptureClient)), uintptr(unsafe.Pointer(&captureClient)))
		if hr != 0 || captureClient == 0 {
			log.Printf("[Audio] GetService IAudioCaptureClient falhou (0x%x)", hr)
			return
		}
		capVtbl := *(*[]uintptr)(unsafe.Pointer(&struct {
			addr uintptr
			len  int
			cap  int
		}{addr: *(*uintptr)(unsafe.Pointer(captureClient)), len: 10, cap: 10}))

		// Start capturing
		syscall.SyscallN(acVtbl[10], audioClient)
		defer syscall.SyscallN(acVtbl[11], audioClient) // Stop

		log.Printf("[Audio] WASAPI Loopback ativo: %d Hz, %d canais, %d bits", pwfx.nSamplesPerSec, pwfx.nChannels, pwfx.wBitsPerSample)

		channels := int(pwfx.nChannels)
		if channels < 1 {
			channels = 2
		}
		isFloat := (pwfx.wBitsPerSample == 32)
		srcRate := int(pwfx.nSamplesPerSec)
		if srcRate <= 0 {
			srcRate = 48000
		}

		pcmBuf := make([]byte, 8192)
		tempLeft := make([]int16, 2048)
		tempRight := make([]int16, 2048)

		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				var nextPacketSize uint32
				syscall.SyscallN(capVtbl[5], captureClient, uintptr(unsafe.Pointer(&nextPacketSize)))

				for nextPacketSize > 0 {
					var pData uintptr
					var numFramesRead uint32
					var flags uint32
					hr, _, _ := syscall.SyscallN(capVtbl[3], captureClient, uintptr(unsafe.Pointer(&pData)), uintptr(unsafe.Pointer(&numFramesRead)), uintptr(unsafe.Pointer(&flags)), 0, 0)

					if hr == 0 && numFramesRead > 0 && pData != 0 {
						if (flags & AUDCLNT_BUFFERFLAGS_SILENT) == 0 {
							nFrames := int(numFramesRead)
							if len(tempLeft) < nFrames {
								tempLeft = make([]int16, nFrames)
								tempRight = make([]int16, nFrames)
							}

							totalSamples := nFrames * channels
							if isFloat {
								floatSlice := (*[1 << 26]float32)(unsafe.Pointer(pData))[:totalSamples:totalSamples]
								for f := 0; f < nFrames; f++ {
									lf := floatSlice[f*channels]
									if lf > 1.0 {
										lf = 1.0
									} else if lf < -1.0 {
										lf = -1.0
									}
									tempLeft[f] = int16(lf * 32767.0)

									rf := lf
									if channels > 1 {
										rf = floatSlice[f*channels+1]
										if rf > 1.0 {
											rf = 1.0
										} else if rf < -1.0 {
											rf = -1.0
										}
									}
									tempRight[f] = int16(rf * 32767.0)
								}
							} else if pwfx.wBitsPerSample == 16 {
								int16Slice := (*[1 << 26]int16)(unsafe.Pointer(pData))[:totalSamples:totalSamples]
								for f := 0; f < nFrames; f++ {
									tempLeft[f] = int16Slice[f*channels]
									if channels > 1 {
										tempRight[f] = int16Slice[f*channels+1]
									} else {
										tempRight[f] = tempLeft[f]
									}
								}
							}

							if srcRate == 48000 {
								outBytes := nFrames * 4
								if len(pcmBuf) < outBytes {
									pcmBuf = make([]byte, outBytes)
								}
								for f := 0; f < nFrames; f++ {
									binary.LittleEndian.PutUint16(pcmBuf[f*4:f*4+2], uint16(tempLeft[f]))
									binary.LittleEndian.PutUint16(pcmBuf[f*4+2:f*4+4], uint16(tempRight[f]))
								}
								onPCM(pcmBuf[:outBytes])
							} else {
								outFrames := (nFrames * 48000) / srcRate
								if outFrames > 0 {
									outBytes := outFrames * 4
									if len(pcmBuf) < outBytes {
										pcmBuf = make([]byte, outBytes)
									}
									for of := 0; of < outFrames; of++ {
										sf := (of * srcRate) / 48000
										if sf >= nFrames {
											sf = nFrames - 1
										}
										binary.LittleEndian.PutUint16(pcmBuf[of*4:of*4+2], uint16(tempLeft[sf]))
										binary.LittleEndian.PutUint16(pcmBuf[of*4+2:of*4+4], uint16(tempRight[sf]))
									}
									onPCM(pcmBuf[:outBytes])
								}
							}
						}
						syscall.SyscallN(capVtbl[4], captureClient, uintptr(numFramesRead))
					}
					syscall.SyscallN(capVtbl[5], captureClient, uintptr(unsafe.Pointer(&nextPacketSize)))
				}
			}
		}
	}()

	return nil
}
