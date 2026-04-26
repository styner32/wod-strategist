#!/usr/bin/env ruby

output_file = "merged_output.mov"
list_file = "files_to_merge.txt"

# Find all .mov files, ignoring the output file if it already exists
files = Dir.glob("*.mov").reject { |f| f == output_file }

if files.empty?
  puts "No .mov files found in the current directory."
  exit
end

# Sort files by creation time (oldest first)
# Uses birthtime, rescues to mtime for older Linux kernels that don't support birthtime
files.sort_by! { |f| File.birthtime(f) rescue File.mtime(f) }

# Create the text file required by FFmpeg's concat demuxer
File.open(list_file, "w") do |file|
  files.each do |f|
    # Escape any single quotes in the filename so FFmpeg doesn't break
    safe_name = f.gsub("'", "'\\''")
    file.puts("file '#{safe_name}'")
  end
end

puts "Found #{files.size} files. Merging..."

# Execute the FFmpeg command
# -f concat: Uses the concat demuxer
# -safe 0: Allows absolute and relative paths
# -c copy: Copies the video/audio streams without re-encoding
success = system("ffmpeg -f concat -safe 0 -i #{list_file} -c copy #{output_file}")

# Clean up the temporary text file
File.delete(list_file) if File.exist?(list_file)

if success
  puts "Successfully merged into #{output_file}"
else
  puts "An error occurred during the merge process."
end
