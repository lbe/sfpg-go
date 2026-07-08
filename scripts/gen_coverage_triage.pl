#!/usr/bin/env perl
use strict;
use warnings;
use File::Basename;
use File::Path qw(make_path);

my $out_dir = 'tmp';
make_path($out_dir) unless -d $out_dir;

# Determine next sequence number from existing cover-func-*.txt files.
my $next = 1;
if (opendir(my $dh, $out_dir)) {
    while (my $f = readdir($dh)) {
        if ($f =~ /^cover-func-(\d+)\.txt$/) {
            $next = $1 + 1 if $1 >= $next;
        }
    }
    closedir($dh);
}

my $func_file   = "$out_dir/cover-func-$next.txt";
my $zero_file   = "$out_dir/cover-zero-pct-$next.txt";
my $below_file  = "$out_dir/cover-below-80-pct-$next.txt";

# Read coverage output from go tool cover -func=coverage.out (or stdin).
my @lines;
if (@ARGV && $ARGV[0] ne '-') {
    my $cmd = $ARGV[0];
    open(my $fh, '-|', $cmd) or die "cannot run $cmd: $!\n";
    @lines = <$fh>;
    close($fh);
} else {
    @lines = <STDIN>;
}

chomp @lines;

# Write full function coverage with line numbers.
open(my $func_fh, '>', $func_file) or die "cannot write $func_file: $!\n";
for (my $i = 0; $i < @lines; $i++) {
    print $func_fh ($i + 1), "\t", $lines[$i], "\n";
}
close($func_fh);

# Filter helpers.
sub is_excluded {
    my ($line) = @_;
    return 1 if $line =~ m{/testutil/};
    return 1 if $line =~ m{scripts/validate-hyperscript\.go:};
    return 1 if $line =~ m{/_test\.go:};
    return 0;
}

sub parse_percent {
    my ($line) = @_;
    if ($line =~ /(\d+(?:\.\d+)?)%\s*$/) {
        return $1 + 0;
    }
    return undef;
}

my @zero;
my @below;
foreach my $line (@lines) {
    next if $line =~ /^total:/;
    next if is_excluded($line);
    my $pct = parse_percent($line);
    next unless defined $pct;
    if ($pct == 0.0) {
        push @zero, $line;
    } elsif ($pct < 80.0) {
        push @below, $line;
    }
}

# Write zero-coverage file (no line numbers, matching existing triage format).
open(my $zero_fh, '>', $zero_file) or die "cannot write $zero_file: $!\n";
foreach my $line (@zero) {
    print $zero_fh $line, "\n";
}
close($zero_fh);

# Write below-80 file (no line numbers, matching existing triage format).
open(my $below_fh, '>', $below_file) or die "cannot write $below_file: $!\n";
foreach my $line (@below) {
    print $below_fh $line, "\n";
}
close($below_fh);

print "Generated:\n";
print "  $func_file\n";
print "  $zero_file\n";
print "  $below_file\n";
