#!/usr/bin/perl
# Byte-transparent TCP proxy: 0.0.0.0:9223 -> 127.0.0.1:9222.
# Chrome 151 DevTools binds localhost only; docker-proxy is not localhost.
# Copied into selenoid-pool (not a symlink to greedy-guru).
use strict;
use warnings;
use IO::Select;
use IO::Socket::INET;

$SIG{CHLD} = "IGNORE";

my $listen = IO::Socket::INET->new(
    LocalAddr => "0.0.0.0",
    LocalPort => 9223,
    Proto     => "tcp",
    Listen    => 32,
    Reuse     => 1,
) or die "listen 9223: $!";

while (my $client = $listen->accept) {
    my $pid = fork;
    if (!defined $pid) {
        close $client;
        next;
    }
    if ($pid) {
        close $client;
        next;
    }
    my $up = IO::Socket::INET->new(
        PeerAddr => "127.0.0.1",
        PeerPort => 9222,
        Proto    => "tcp",
    ) or exit 1;
    my $sel = IO::Select->new($client, $up);
    while (my @ready = $sel->can_read) {
        for my $fh (@ready) {
            my $n = sysread($fh, my $buf, 8192);
            exit 0 if !defined $n || $n == 0;
            my $out = $fh == $client ? $up : $client;
            syswrite($out, $buf) or exit 0;
        }
    }
    exit 0;
}
