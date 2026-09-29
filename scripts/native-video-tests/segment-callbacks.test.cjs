const assert = require('node:assert/strict')
const fs = require('node:fs')
const path = require('node:path')
const vm = require('node:vm')
const test = require('node:test')

function cameraFixture(os = 'ios') {
  const calls = []
  class PureComponent { constructor(props) { this.props = props } setState(value) { Object.assign(this.state, value) } }
  class CameraRuntimeError extends Error { constructor(code, message) { super(message); this.code = code } }
  const context = { exports: {}, require(name) {
    if (name === 'react') return { PureComponent, createRef: () => ({ current: {} }) }
    if (name === 'react-native') return { StyleSheet: { create: x => x }, Platform: { OS: os }, findNodeHandle: () => 1 }
    if (name === './CameraError') return { CameraRuntimeError, tryParseNativeCameraError: x => x }
    if (name === './NativeCameraModule') return { CameraModule: { startRecording: (...args) => calls.push(args) } }
    if (name === './RotationHelper') return { RotationHelper: class {} }
    return {}
  } }
  const source = path.join(__dirname, '../../node_modules/react-native-vision-camera/lib/commonjs/Camera.js')
  vm.runInNewContext(fs.readFileSync(source, 'utf8'), context, { filename: source })
  return { camera: new context.exports.Camera({}), calls }
}

test('terminal native segment list replays missing events once before finished', () => {
  const { camera, calls } = cameraFixture()
  const seen = []
  camera.startRecording({ segmented: true, path: '/durable/run',
    onRecordingSegment: s => seen.push(s.index), onRecordingSourceFinalized: () => seen.push('source'),
    onRecordingFinished: () => seen.push('finished'), onRecordingError: e => { throw e } })
  const [, options, complete] = calls[0]
  const recordingId = options.segmentedRecordingId
  const first = { index: 1, status: 'ready' }, last = { index: 2, status: 'failed' }
  camera.onRecordingSegment({ nativeEvent: { ...first, recordingId } })
  camera.onRecordingSegment({ nativeEvent: { ...first, recordingId } })
  camera.onRecordingSourceFinalized({ nativeEvent: { path: '/durable/run/original.mp4', recordingId } })
  complete({ segments: [first, last] })
  camera.onRecordingSegment({ nativeEvent: { ...last, recordingId } })
  assert.deepEqual(seen, [1, 'source', 2, 'finished'])
  assert.equal(camera.segmentCallbacks.size, 0)
  assert.equal(camera.sourceCallbacks.size, 0)
  assert.equal(camera.deliveredSegments.size, 0)
})

test('segmented validation does not register callbacks on unsupported platform or missing finish callback', () => {
  const { camera, calls } = cameraFixture('android')
  assert.throws(() => camera.startRecording({ segmented: true, path: '/durable/run',
    onRecordingSegment: () => {}, onRecordingFinished: () => {}, onRecordingError: () => {} }), /only supported on iOS/)
  assert.equal(calls.length, 0)
  assert.equal(camera.segmentCallbacks.size, 0)
  const fixture = cameraFixture()
  assert.throws(() => fixture.camera.startRecording({ segmented: true, path: '/durable/run',
    onRecordingSegment: () => {}, onRecordingError: () => {} }), /functions were not set/)
  assert.equal(fixture.camera.segmentCallbacks.size, 0)
})

test('legacy recording still finishes through its single native callback', () => {
  const { camera, calls } = cameraFixture()
  let final
  camera.startRecording({ onRecordingFinished: value => { final = value }, onRecordingError: e => { throw e } })
  const [, options, complete] = calls[0]
  assert.equal(options.segmentedRecordingId, undefined)
  const video = { path: '/legacy.mov' }
  complete(video)
  assert.equal(final, video)
})

test('old drained callback does not clear a resumed run flash state', () => {
  const { camera, calls } = cameraFixture()
  const options = { segmented: true, path: '/durable/run', flash: 'on',
    onRecordingSegment: () => {}, onRecordingFinished: () => {}, onRecordingError: e => { throw e } }
  camera.startRecording(options)
  camera.startRecording(options) // Native source-ready handoff allows the second run.
  calls[0][2]({ segments: [] })
  assert.equal(camera.state.isRecordingWithFlash, true)
  calls[1][2]({ segments: [] })
  assert.equal(camera.state.isRecordingWithFlash, false)
})
