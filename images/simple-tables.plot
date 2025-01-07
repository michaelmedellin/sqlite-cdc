set terminal svg background rgb "#FFFFFF"
set title "Simple Tables With With Triggers"
set border lc rgb 'black'
set key outside right vertical tc rgb 'black'
set linetype 1 lc rgb 'black'
set linetype 2 lc rgb 'blue'
set linetype 3 lc rgb 'dark-grey'


set style fill solid
set boxwidth .5


set xlabel 'Columns' tc rgb 'black'
set style data histograms

set ylabel 'Percent Worse (%)' tc rgb 'black'
set yrange [0.0:205.0]
set ytics nomirror 50

$data << EOF
2  97 100 113
4  96 96 105
8  93 99 111
16 99 111 127
32 106 158 153
64 105 179 203
EOF

plot \
    "$data" using 2:xtic(1) title "INSERT", \
    "" using 3 title "UPDATE", \
    "" using 4 title "DELETE"

