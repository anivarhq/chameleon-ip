import 'dart:async';
import 'dart:convert';
import 'dart:io';

/// What the engine last reported.
class Status {
  const Status({
    this.streamUrl = '',
    this.camera = '',
    this.viewers = 0,
    this.notes = '',
    this.error = '',
    this.running = false,
  });

  final String streamUrl;
  final String camera;
  final int viewers;
  final String notes;
  final String error;
  final bool running;

  factory Status.fromJson(Map<String, dynamic> json) => Status(
        streamUrl: json['stream_url'] as String? ?? '',
        camera: json['camera'] as String? ?? '',
        viewers: json['viewers'] as int? ?? 0,
        notes: json['notes'] as String? ?? '',
        error: json['error'] as String? ?? '',
        running: true,
      );
}

/// What the window needs from an engine. The window depends on this rather
/// than on the process, so it can be tested without spawning one.
abstract class CameraEngine {
  Stream<Status> get statuses;
  Future<List<String>> cameras();
  Future<void> start({String? camera});
  Future<void> stop();
}

/// Drives the `chameleon` engine, which does the capturing and serving.
///
/// The engine is a separate process rather than a library: it is the same
/// binary on Windows, macOS and Linux, it already runs headless, and a crash
/// in the camera pipeline cannot take the window down with it.
class Engine implements CameraEngine {
  Engine({String? executable}) : _executable = executable ?? _defaultExecutable();

  final String _executable;
  Process? _process;

  final _statuses = StreamController<Status>.broadcast();
  @override
  Stream<Status> get statuses => _statuses.stream;

  bool get isRunning => _process != null;

  @override
  Future<List<String>> cameras() async {
    final result = await Process.run(_executable, ['-list']);
    return const LineSplitter()
        .convert(result.stdout as String)
        .map((line) => line.trim())
        .where((line) => line.isNotEmpty)
        .toList();
  }

  @override
  Future<void> start({String? camera}) async {
    if (_process != null) return;
    final process = await Process.start(_executable, [
      '-json',
      if (camera != null && camera.isNotEmpty) ...['-device', camera],
    ]);
    _process = process;

    process.stdout.transform(utf8.decoder).transform(const LineSplitter()).listen((line) {
      if (!line.startsWith('{')) return;
      try {
        _statuses.add(Status.fromJson(jsonDecode(line) as Map<String, dynamic>));
      } catch (_) {
        // A partial line while the engine is starting is not worth reporting.
      }
    });

    // The engine writes real failures to stderr; surface them rather than
    // leaving the window saying "starting" forever.
    process.stderr.transform(utf8.decoder).transform(const LineSplitter()).listen((line) {
      if (line.trim().isEmpty) return;
      _statuses.add(Status(error: line.trim()));
    });

    unawaited(process.exitCode.then((_) {
      _process = null;
      _statuses.add(const Status());
    }));
  }

  @override
  Future<void> stop() async {
    _process?.kill();
    _process = null;
    _statuses.add(const Status());
  }

  /// The engine ships next to the app, so it is found without configuration.
  static String _defaultExecutable() {
    final name = Platform.isWindows ? 'chameleon.exe' : 'chameleon';
    final beside = File('${File(Platform.resolvedExecutable).parent.path}${Platform.pathSeparator}$name');
    return beside.existsSync() ? beside.path : name;
  }
}
