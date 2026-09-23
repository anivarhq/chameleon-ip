import 'package:chameleon_ip/main.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  testWidgets('opens idle, offering to start the camera', (tester) async {
    await tester.pumpWidget(const ChameleonApp());
    await tester.pump();

    expect(find.text('Not streaming'), findsOneWidget);
    expect(find.widgetWithText(FilledButton, 'Start camera'), findsOneWidget);
    // Nothing to add to an NVR until something is actually streaming.
    expect(find.text('Add this to your NVR'), findsNothing);
  });
}
