import 'dart:async';

import 'package:chameleon_ip/engine.dart';
import 'package:chameleon_ip/main.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

/// Stands in for the engine process, so the window can be tested without
/// spawning one.
class FakeEngine implements CameraEngine {
  final _controller = StreamController<Status>.broadcast();
  final started = <String?>[];
  var stopped = false;

  @override
  Stream<Status> get statuses => _controller.stream;

  @override
  Future<List<String>> cameras() async => ['Integrated Camera', 'Doorbell'];

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

  testWidgets('opens idle and offers the cameras it found', (tester) async {
    final engine = FakeEngine();
    await tester.pumpWidget(ChameleonApp(engine: engine));
    await tester.pumpAndSettle();

    expect(find.text('Not streaming'), findsOneWidget);
    expect(find.widgetWithText(FilledButton, 'Start camera'), findsOneWidget);
    // Nothing to add to an NVR until something is streaming.
    expect(find.text('Add this to your NVR'), findsNothing);
    expect(find.text('Integrated Camera'), findsOneWidget);
  });

  testWidgets('shows the address and who is watching once started', (tester) async {
    final engine = FakeEngine();
    await tester.pumpWidget(ChameleonApp(engine: engine));
    await tester.pumpAndSettle();

    await tester.tap(find.widgetWithText(FilledButton, 'Start camera'));
    await tester.pumpAndSettle();

    expect(engine.started, ['Integrated Camera']);
    expect(find.text('2 watching'), findsOneWidget);
    expect(find.text('Add this to your NVR'), findsOneWidget);
    expect(find.text('rtsp://admin:secret@192.168.1.5:8554/main'), findsOneWidget);

    await tester.tap(find.widgetWithText(FilledButton, 'Stop'));
    await tester.pumpAndSettle();
    expect(engine.stopped, isTrue);
    expect(find.text('Not streaming'), findsOneWidget);
  });
}
