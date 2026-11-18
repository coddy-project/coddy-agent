Feature: Coddy on FreeBSD
  FreeBSD is a supported platform beside Linux, macOS, Windows and Android:
  the release publishes an archive for it, `coddy update` replaces the running
  binary with the next one, and the parts that read the kernel rather than the
  standard library have a FreeBSD implementation instead of the stub that
  answers "not supported on this platform".

  The kernel interfaces are the BSD ones, not Linux's: a peer's credentials
  come from LOCAL_PEERCRED as a struct xucred, free space from statfs with
  f_bsize, and a process's parent from the kern.proc.pid sysctl, because
  procfs is not mounted on a FreeBSD system by default. Everything above that
  is the same freedesktop stack a Linux desktop runs, so the X11 window, the
  D-Bus tray and the XDG autostart entry of `coddy desktop` are the Linux ones.

  Scenario Outline: The release publishes an archive for FreeBSD
    Given the release workflow of the repository
    Then it cross-builds coddy for "freebsd" and "<arch>"
    And a FreeBSD machine on "<arch>" asks for "coddy_1.2.3_freebsd_<arch>.tar.gz" to update to 1.2.3

    Examples:
      | arch  |
      | amd64 |
      | arm64 |

  Scenario: The whole shipped binary compiles for FreeBSD
    Given the tag set the release builds
    When the tree is cross-compiled for FreeBSD
    Then every package builds

  Scenario: The local endpoint verifies who is on the other end
    When Coddy is built for FreeBSD
    Then the package "internal/localipc" compiles "peer_xucred.go"
    And it does not compile "peer_other.go"

  Scenario: Kimi credentials are stored under a lock that survives a crash
    When Coddy is built for FreeBSD
    Then the package "internal/llm" compiles "kimi_secure_unix.go"
    And it does not compile "kimi_secure_other.go"

  Scenario: The status page knows how much disk is left
    When Coddy is built for FreeBSD
    Then the package "internal/platform" compiles "diskfree_bsd.go"
    And it does not compile "diskfree_other.go"

  Scenario: The desktop window is the X11 one a Linux desktop gets
    When Coddy is built for FreeBSD with the "desktop" tag
    Then the package "external/desktop" compiles "window_x11.go"
    And the package "external/desktop" compiles "window_parent_freebsd.go"
    And the package "external/desktop" compiles "tray_sni.go"
    And the package "external/desktop" compiles "autostart_xdg.go"
    And the package "external/desktop" compiles "browser_xdg.go"
    And it does not compile "window_other.go"
