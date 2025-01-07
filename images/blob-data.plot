set terminal svg background rgb "#FFFFFF"
set title "Encoding Cost Of BLOB Data"
set border lc rgb 'black'
set key below horizontal tc rgb 'black'
set linetype 1 lc rgb 'black'

set style fill solid
set boxwidth .5


set xlabel 'Byte Size' tc rgb 'black'
set logscale x 2
set xtic rotate by 90 right

set ylabel 'Percent Worse Than 16 bytes (%)' tc rgb 'black'
set yrange [0.0:1000.0]
set ytics nomirror 50

$data << EOF
16  0
64 67.0
256 64.8
1024 69.2
4096 74.0
16384 85.4
32768 102.3
65536 126.4
131072 187.3
262144 286.9
524288 510.1
1048576 936.9
EOF

plot \
    "$data" using 1:2:xtic(1) title "" with boxes axes x1y1

