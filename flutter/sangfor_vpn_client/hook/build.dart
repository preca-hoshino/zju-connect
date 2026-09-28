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
///   4. `<repo>/build/` for a host build (developer convenience).
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
    final source = _locateLibrary(input, target, fileName, isStatic, logger);

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

  // The repository layout: <repo>/build/<file> for a host build. The package
  // lives at <repo>/flutter/sangfor_vpn_client, so it is two levels up.
  final hostBuild = File.fromUri(packageRoot.resolve('../../build/$fileName'));
  if (hostBuild.existsSync()) {
    logger.info('using repository build at ${hostBuild.absolute.path}');
    return hostBuild.absolute.uri;
  }

  throw StateError(
    'No native library found for $target.\n'
    'Build one with:\n'
    '  binding/build.sh host      # current platform\n'
    '  binding/build.sh android   # all Android ABIs\n'
    '  binding/build.sh ios       # iOS static archives\n'
    'Then either copy the artifact to '
    'flutter/sangfor_vpn_client/native/$target/$fileName, set '
    'ZJU_CONNECT_LIBRARY_DIR, or declare '
    'hooks.user_defines.sangfor_vpn_client.library.$target in the consuming '
    'app\'s pubspec.yaml.',
  );
}
