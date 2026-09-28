/// Build hook for the sangfor_vpn_client package.
///
/// Unlike a typical FFI plugin, the native code here is not C that the Dart SDK
/// compiles: it is a Go shared library produced by `binding/build.sh` in the
/// parent repository. This hook therefore *locates* the prebuilt artifact for
/// the requested target and registers it with the SDK, which copies it into the
/// application bundle.
///
/// Resolution order for a given target:
///
///   1. `hooks.user_defines.sangfor_vpn_client.library.<os>-<arch>` in the
///      consuming app's pubspec.yaml — an explicit path wins.
///   2. `ZJU_CONNECT_LIBRARY_DIR` environment variable, if set.
///   3. `flutter/sangfor_vpn_client/native/<os>-<arch>/` inside this package,
///      used when the library is checked in or downloaded ahead of time.
///   4. A downloaded release archive, cached under `ZJU_CONNECT_CACHE_DIR` or
///      `.dart_tool/zju_connect_cache`, when `download-url` is configured.
///   5. `<repo>/build/native/<os>-<arch>/` for a developer host build.
///
/// If nothing matches, the hook fails with a message describing how to produce
/// the artifact, which is far more actionable than a missing-symbol crash later.
///
/// On iOS there is no dynamic library to bundle: the Go code is compiled as a
/// static archive and linked into the app. Set the custom define
/// `ios-static-library` (or place the archive under `native/ios-<arch>/`) and
/// this hook will emit a static-linking asset instead.
library;

import 'dart:io';

import 'package:archive/archive.dart';
import 'package:code_assets/code_assets.dart';
import 'package:hooks/hooks.dart';
import 'package:logging/logging.dart';

/// Maps a target to the conventional file name of the built library.
const _libraryFileNames = {
  OS.windows: 'zju_connect.dll',
  OS.macOS: 'libzju_connect.dylib',
  OS.linux: 'libzju_connect.so',
  OS.android: 'libzju_connect.so',
  OS.iOS: 'libzju_connect.a',
};

void main(List<String> args) async {
  final logger = Logger('sangfor_vpn_client')
    // The SDK surfaces hook output on the console; print is the intended sink.
    // ignore: avoid_print
    ..onRecord.listen((r) => print('[hook] ${r.message}'));

  await build(args, (input, output) async {
    if (!input.config.buildCodeAssets) {
      // The SDK invokes hooks once per asset type; this package only emits code.
      return;
    }

    final target = _targetOf(input);
    logger.info('resolving native library for $target');

    final fileName = _libraryFileNames[input.config.code.targetOS];
    if (fileName == null) {
      throw UnsupportedError(
        'sangfor_vpn_client does not support target ${input.config.code.targetOS}.',
      );
    }

    final isStatic = input.config.code.targetOS == OS.iOS;
    final source = _locateLibrary(
      input,
      output,
      target,
      fileName,
      isStatic,
      logger,
    );

    // Copy into the hook output directory; the SDK bundles from there. The
    // file name is preserved because the dynamic linker keys on it.
    final destination = input.outputDirectory.resolve(fileName);
    await File.fromUri(source).copy(destination.toFilePath());

    // A change to the source artifact must invalidate the build cache.
    output.dependencies.add(source);

    output.assets.code.add(
      CodeAsset(
        package: input.packageName,
        // The id must match @ffi.DefaultAsset in the generated bindings. It is
        // not a file name, so it stays stable across platforms.
        name: 'zju_connect.dart',
        file: destination,
        linkMode: isStatic ? StaticLinking() : DynamicLoadingBundled(),
      ),
    );
    logger.info('registered $fileName (${isStatic ? 'static' : 'dynamic'})');
  });
}

/// Builds the `<os>-<arch>` key used for directory and user-define lookups.
String _targetOf(BuildInput input) {
  final os = switch (input.config.code.targetOS) {
    OS.android => 'android',
    OS.iOS => 'ios',
    OS.linux => 'linux',
    OS.macOS => 'macos',
    OS.windows => 'windows',
    final other => other.name,
  };
  final arch = switch (input.config.code.targetArchitecture) {
    Architecture.arm64 => 'arm64',
    Architecture.arm => 'arm',
    Architecture.ia32 => 'ia32',
    Architecture.x64 => 'x64',
    final other => other.name,
  };
  return '$os-$arch';
}

Uri _locateLibrary(
  BuildInput input,
  BuildOutputBuilder output,
  String target,
  String fileName,
  bool isStatic,
  Logger logger,
) {
  final override = input.userDefines.path(
    isStatic ? 'ios-static-library' : 'library.$target',
  );
  if (override != null) {
    final file = File.fromUri(override);
    if (!file.existsSync()) {
      throw StateError(
        'Configured library does not exist: ${override.toFilePath()}',
      );
    }
    logger.info('using user-defined library at ${override.toFilePath()}');
    return override;
  }

  final envDir = Platform.environment['ZJU_CONNECT_LIBRARY_DIR'];
  if (envDir != null && envDir.isNotEmpty) {
    final file = File.fromUri(Directory(envDir).uri.resolve(fileName));
    if (file.existsSync()) {
      logger.info('using ZJU_CONNECT_LIBRARY_DIR at $envDir');
      return file.uri;
    }
  }

  // Resolve relative to the package root as URIs: string concatenation would
  // mix separators on Windows.
  final packageRoot = input.packageRoot;

  final bundled = File.fromUri(packageRoot.resolve('native/$target/$fileName'));
  if (bundled.existsSync()) {
    logger.info('using bundled library at ${bundled.path}');
    return bundled.uri;
  }

  final downloaded = _fromDownloadCache(
    input,
    output,
    target,
    fileName,
    logger,
  );
  if (downloaded != null) return downloaded;

  // The repository layout: <repo>/build/native/<target>/<file> for a host
  // build. The package lives at <repo>/flutter/sangfor_vpn_client.
  final repoBuild = File.fromUri(
    packageRoot.resolve('../../build/native/$target/$fileName'),
  );
  if (repoBuild.existsSync()) {
    logger.info('using repository build at ${repoBuild.absolute.path}');
    return repoBuild.absolute.uri;
  }

  throw StateError(
    'No native library found for $target.\n'
    '\n'
    'Either configure a release archive to download:\n'
    '  hooks:\n'
    '    user_defines:\n'
    '      sangfor_vpn_client:\n'
    '        download-url: https://github.com/OWNER/REPO/releases/download/vX.Y.Z/\n'
    '\n'
    'Or build one locally with:\n'
    '  binding/build.sh host      # current platform\n'
    '  binding/build.sh android   # all Android ABIs\n'
    '  binding/build.sh ios       # iOS static archives\n'
    '\n'
    'Then copy the artifact to '
    'flutter/sangfor_vpn_client/native/$target/$fileName, set '
    'ZJU_CONNECT_LIBRARY_DIR, or declare '
    'hooks.user_defines.sangfor_vpn_client.library.$target in the consuming '
    'app\'s pubspec.yaml.',
  );
}

/// Downloads and unpacks a release archive when one is configured.
///
/// Why a download at all: pub.dev rejects packages larger than 100 MB, and the
/// Go library is tens of MB per platform, so the artifacts cannot simply be
/// committed. The CI publishes one zip per release and this hook fetches it on
/// demand, caching the result so a rebuild is cheap.
///
/// Configuration, via `hooks.user_defines.sangfor_vpn_client` in the consuming
/// app's pubspec.yaml:
///
/// ```yaml
/// hooks:
///   user_defines:
///     sangfor_vpn_client:
///       download-url: https://github.com/OWNER/REPO/releases/download/v0.1.0/
/// ```
///
/// `download-url` is a base URL; the hook appends `zju-connect-<os>-<arch>.zip`.
/// Each archive must contain the library at a path whose base name is the
/// expected file name.
///
/// The unpacked files land in `ZJU_CONNECT_CACHE_DIR`, or
/// `<package>/.dart_tool/zju_connect_cache` by default.
Uri? _fromDownloadCache(
  BuildInput input,
  BuildOutputBuilder output,
  String target,
  String fileName,
  Logger logger,
) {
  final baseUrl = input.userDefines['download-url'];
  if (baseUrl is! String || baseUrl.isEmpty) return null;

  final separator = Platform.pathSeparator;
  final cacheRoot =
      Platform.environment['ZJU_CONNECT_CACHE_DIR'] ??
      input.packageRoot
          .resolve('.dart_tool/zju_connect_cache/')
          .toFilePath(windows: Platform.isWindows);
  final cached = File('$cacheRoot$separator$target$separator$fileName');
  if (cached.existsSync()) {
    logger.info('using cached download at ${cached.path}');
    output.dependencies.add(cached.uri);
    return cached.uri;
  }

  final trimmed = baseUrl.replaceAll(RegExp(r'/+$'), '');
  final url = '$trimmed/zju-connect-$target.zip';
  logger.info('downloading $url');

  // Keep the download next to the extracted library so a partial run can be
  // inspected, and so the cache can be cleared as one unit.
  final downloadDir =
      '$cacheRoot$separator'
      'download';
  final archiveFile = File('$downloadDir$separator$target.zip');
  try {
    _download(url, archiveFile);
    _extractLibrary(archiveFile, cached, fileName);
  } on Object catch (error) {
    logger.warning('download from $url failed: $error');
    return null;
  }

  logger.info('extracted to ${cached.path}');
  output.dependencies.add(cached.uri);
  return cached.uri;
}

/// Fetches [url] into [destination] using the platform's downloader.
///
/// Shelling out to curl keeps this dependency-free and behaves the same on the
/// Linux, macOS and Windows runners.
void _download(String url, File destination) {
  destination.parent.createSync(recursive: true);
  final result = Process.runSync('curl', [
    '--fail',
    '--location',
    '--silent',
    '--show-error',
    '--retry',
    '3',
    '--output',
    destination.path,
    url,
  ]);
  if (result.exitCode != 0) {
    throw StateError('curl exited with ${result.exitCode}: ${result.stderr}');
  }
}

/// Extracts the library from [archiveFile] into [destination].
///
/// Any directory structure inside the archive is flattened: only the entry
/// whose base name is [fileName] is kept. That makes the hook tolerant of the
/// CI packaging the artifacts flat or under a `native/<target>/` prefix.
void _extractLibrary(File archiveFile, File destination, String fileName) {
  final archive = ZipDecoder().decodeBytes(archiveFile.readAsBytesSync());
  for (final entry in archive.files) {
    if (!entry.isFile) continue;
    final baseName = entry.name.replaceAll(r'\', '/').split('/').last;
    if (baseName != fileName) continue;
    destination.parent.createSync(recursive: true);
    destination.writeAsBytesSync(entry.content as List<int>);
    return;
  }
  throw StateError('$fileName not found inside ${archiveFile.path}');
}
