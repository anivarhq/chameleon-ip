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
    _controller.add(const Status());
  }
}

void main() {
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
