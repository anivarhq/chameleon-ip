import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:chameleon_ip/engine.dart';
import 'package:chameleon_ip/main.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

/// A 1x1 PNG: the smallest picture the window can decode and show.
final _pixel = base64Decode(
    'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==');

/// Stands in for the engine process, so the window can be tested without
/// spawning one.
class FakeEngine implements CameraEngine {
  final _controller = StreamController<Status>.broadcast();
  final _frames = StreamController<Uint8List>.broadcast();
  final started = <String?>[];
  var stopped = false;

  /// When set, listing cameras fails the way the real engine does.
  Object? listError;

  @override
  Stream<Status> get statuses => _controller.stream;

  @override
  Stream<Uint8List> get frames => _frames.stream;

  @override
  Future<List<String>> cameras() async {
    if (listError != null) throw listError!;
    return ['Integrated Camera', 'Doorbell'];
  }

  @override
  Future<void> start({String? camera}) async {
    started.add(camera);
    _controller.add(const Status(
      streamUrl: 'rtsp://admin:secret@192.168.1.5:8554/main',
      camera: 'Integrated Camera',
      viewers: 2,
      running: true,
    ));
  }

  @override
  Future<void> stop() async {
    stopped = true;
    if (!_controller.isClosed) _controller.add(const Status());
  }

  /// Lets the test push a status as if the engine had sent one.
  void emit(Status status) => _controller.add(status);

  /// Lets the test push a picture as if the camera had produced one.
  void showFrame(Uint8List frame) => _frames.add(frame);

  bool get hasListeners => _controller.hasListener;
}

void main() {
  testWidgets('lets go of the engine when the window closes', (tester) async {
    final engine = FakeEngine();
    await tester.pumpWidget(ChameleonApp(engine: engine));
    await tester.pumpAndSettle();
    expect(engine.hasListeners, isTrue);

    // Replace the window with something else, as closing it does.
    await tester.pumpWidget(const SizedBox());
    await tester.pumpAndSettle();

    expect(engine.hasListeners, isFalse, reason: 'still listening after the window went away');
    expect(engine.stopped, isTrue, reason: 'the engine was left running with the camera on');

    // A late status must not reach the dead widget; without the cancel this
    // throws "setState() called after dispose()".
    engine.emit(const Status(running: true, viewers: 1));
    await tester.pumpAndSettle();
  });

  testWidgets('opens with the camera off, and nothing about recorders', (tester) async {
    final engine = FakeEngine();
    await tester.pumpWidget(ChameleonApp(engine: engine));
    await tester.pumpAndSettle();

    expect(find.text('Camera is off'), findsOneWidget);
    expect(find.widgetWithText(FilledButton, 'Turn on camera'), findsOneWidget);
    expect(find.text('Integrated Camera'), findsOneWidget);
    // Sharing is not part of looking at the camera.
    expect(find.text('Use with a recorder'), findsNothing);
  });

  testWidgets('shows the picture once on; the address only when asked for', (tester) async {
    final engine = FakeEngine();
    await tester.pumpWidget(ChameleonApp(engine: engine));
    await tester.pumpAndSettle();

    await tester.tap(find.widgetWithText(FilledButton, 'Turn on camera'));
    // pump, not pumpAndSettle: the spinner never settles.
    await tester.pump();
    await tester.pump();
    expect(engine.started, ['Integrated Camera']);
    expect(find.text('Starting the camera…'), findsOneWidget);

    engine.showFrame(_pixel);
    await tester.pumpAndSettle();
    expect(find.byType(Image), findsOneWidget);
    expect(find.text('Live · 2 other devices watching'), findsOneWidget);

    // Offered, but folded away: nobody has to add it anywhere to see it.
    const url = 'rtsp://admin:secret@192.168.1.5:8554/main';
    expect(find.text('Use with a recorder'), findsOneWidget);
    expect(find.text(url), findsNothing);
    await tester.tap(find.text('Use with a recorder'));
    await tester.pumpAndSettle();
    expect(find.text(url), findsOneWidget);

    await tester.tap(find.widgetWithText(FilledButton, 'Turn off camera'));
    await tester.pumpAndSettle();
    expect(engine.stopped, isTrue);
    expect(find.text('Camera is off'), findsOneWidget);
    expect(find.byType(Image), findsNothing, reason: 'a stopped camera left its last picture up');
  });

  testWidgets('says what the engine refused, not that there is no camera', (tester) async {
    final engine = FakeEngine()
      ..listError = const EngineError('ffmpeg not found. Chameleon IP needs it to read the camera');
    await tester.pumpWidget(ChameleonApp(engine: engine));
    await tester.pumpAndSettle();

    expect(find.textContaining('ffmpeg not found'), findsOneWidget);
    expect(find.textContaining('Could not find the engine'), findsNothing);
  });
}
